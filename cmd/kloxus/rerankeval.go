package main

// kloxus rerank-eval: KLOX-110 checkpoint tooling (spec section 9).
//
//	kloxus rerank-eval list
//	kloxus rerank-eval record --pub <id> --workspace <ws> --out file.json [--rerank]
//
// record ranks the publication's stored stories by (a) FTS, (b)
// embeddings and, with --rerank, (c) embeddings + reranker, and writes
// the candidate pool for hand labelling. It is read-only and never
// touches the rerank cache; --rerank is the one paid recording.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/config"
	"github.com/sthefanocordeiro/kloxus/internal/pipeline"
	"github.com/sthefanocordeiro/kloxus/internal/store"
)

type evalCand struct {
	ID      int64              `json:"id"`
	Title   string             `json:"title"`
	Outlets []string           `json:"outlets"`
	Snippet string             `json:"snippet"`
	Ranks   map[string]int     `json:"ranks"` // 1-based, strategy -> position
	EmbRel  float64            `json:"emb_rel"`
	Rerank  float64            `json:"rerank"`
	Extra   map[string]float64 `json:"-"`
}

type evalDump struct {
	Pub        string             `json:"pub"`
	Topic      string             `json:"topic"`
	Editorial  string             `json:"editorial_line"`
	Reject     string             `json:"reject_terms"`
	Stories    int                `json:"stories"`
	WithVector int                `json:"with_vector"`
	Orders     map[string][]int64 `json:"orders"`
	Pool       []evalCand         `json:"pool"`
	All        []evalCand         `json:"all"`
}

func cmdRerankEval(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: rerank-eval list | record --pub id --workspace ws --out file [--rerank]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := context.Background()
	switch args[0] {
	case "list":
		ov, err := pool.PublicationsOverview(ctx)
		if err != nil {
			return err
		}
		for _, o := range ov {
			fmt.Printf("pub=%d ws=%s stories=%d lang=%s name=%q topic=%q\n", o.ID, o.WorkspaceID, o.Stories, o.Language, o.Name, o.Topic)
		}
		return nil
	case "record":
		fs := flag.NewFlagSet("record", flag.ContinueOnError)
		pubID := fs.String("pub", "", "publication id")
		ws := fs.String("workspace", "", "workspace id")
		out := fs.String("out", "", "output json")
		withRerank := fs.Bool("rerank", false, "also call the Voyage reranker (paid)")
		perStrategy := fs.Int("pool", 20, "top-N of each strategy joins the label pool")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		db := pool.For(*ws)
		pub, err := db.GetPublication(ctx, *pubID)
		if err != nil {
			return err
		}
		stories, err := db.StoriesWithItems(ctx, pub.ID)
		if err != nil {
			return err
		}
		emb := newEmbedder(cfg, pool)
		var rr = newReranker(cfg, pool)
		if !*withRerank {
			rr = nil
		}
		res, err := pipeline.EvalRank(ctx, db, pub, stories, emb, rr)
		if err != nil {
			return err
		}
		byID := map[int64]*store.Story{}
		for i := range stories {
			byID[stories[i].ID] = &stories[i]
		}
		cands := map[int64]*evalCand{}
		var poolOrder []int64
		for _, strat := range []string{"emb_rerank", "emb", "fts"} {
			for n, id := range res.Orders[strat] {
				if c, ok := cands[id]; ok {
					c.Ranks[strat] = n + 1
				} else if n < *perStrategy {
					s := byID[id]
					snip := ""
					if len(s.Items) > 0 {
						snip = s.Items[0].ContentText
						if r := []rune(snip); len(r) > 350 {
							snip = string(r[:350])
						}
					}
					cands[id] = &evalCand{ID: id, Title: s.Title, Outlets: s.Outlets(), Snippet: strings.TrimSpace(snip),
						Ranks: map[string]int{strat: n + 1}, EmbRel: res.EmbRel[id], Rerank: res.RerankScore[id]}
					poolOrder = append(poolOrder, id)
				}
			}
		}
		d := evalDump{Pub: pub.Name, Topic: pub.Topic, Editorial: pub.EditorialLine, Reject: pub.RejectTerms,
			Stories: len(stories), Orders: res.Orders}
		for _, id := range poolOrder {
			c := cands[id]
			for _, strat := range []string{"emb_rerank", "emb", "fts"} {
				for n, oid := range res.Orders[strat] {
					if oid == id {
						c.Ranks[strat] = n + 1
					}
				}
			}
			d.Pool = append(d.Pool, *c)
		}
		for i := range stories {
			s := &stories[i]
			snip := ""
			if len(s.Items) > 0 {
				if r := []rune(s.Items[0].ContentText); len(r) > 220 {
					snip = string(r[:220])
				} else {
					snip = string(r)
				}
			}
			d.All = append(d.All, evalCand{ID: s.ID, Title: s.Title, Outlets: s.Outlets(), Snippet: strings.TrimSpace(snip), Ranks: map[string]int{}})
		}
		b, _ := json.MarshalIndent(d, "", " ")
		return os.WriteFile(*out, b, 0o600)
	}
	return fmt.Errorf("unknown rerank-eval command %q", args[0])
}
