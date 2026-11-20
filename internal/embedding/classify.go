package embedding

// Deterministic item classification (KLOX-93): lexicon-scored topic
// tags over the fixed §7.2 list, a light kind heuristic, and a
// stopword-ratio language guess. Everything here is free — no model
// call — and identical inputs always produce identical labels, which
// is what makes the backfill sweep idempotent. When the AI enrichment
// is enabled it upgrades these rows later (the 'local-%' model tag).
import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Result is the deterministic classification of one item.
type Result struct {
	Topics   []string // ⊂ §7.2 fixed tag list, score-ordered, ≤5
	Kind     string   // ai_kind slug, same vocabulary as the AI enricher
	Language string   // pt | en | '' when undecidable
}

// topicLexicon maps each §7.2 tag to the folded terms that score it.
// Terms are matched as folded substrings bounded on token boundaries —
// a term like "pix" hits "PIX" and "via PIX" but not "pixel".
var topicLexicon = map[string][]string{
	"fintech": {
		"fintech", "pix", "banco digital", "nubank", "open finance",
		"meios de pagamento", "pagamento instantaneo", "cartao de credito",
		"maquininha", "recebiveis", "ted", "boleto",
	},
	"cripto": {
		"cripto", "bitcoin", "criptomoeda", "criptoativo", "blockchain",
		"ethereum", "stablecoin", "token", "btc",
	},
	"regulacao": {
		"regulacao", "regulador", "anatel", "banco central", "bacen",
		"cvm", "anvisa", "aneel", "ans ", "marco regulatorio",
		"consulta publica", "medida provisoria", "decreto", "portaria",
		"projeto de lei", "lei aprovada", "sancionada", "tcu", "cgfn",
	},
	"politica": {
		"eleicao", "eleicoes", "presidente", "governo", "ministro",
		"deputado", "senador", "partido", "votacao", "congresso",
		"planalto", "prefeito", "prefeitura", "governador", "camara",
		"senado", "stf", "tse", "oposicao",
	},
	"economia": {
		"economia", "inflacao", "ipca", "selic", "juros", "pib",
		"desemprego", "cambio", "dolar", "divida publica", "orcamento",
		"fiscal", "arrecadacao", "recesso", "poupanca", "rendimento",
		"tesouro", "combustivel", "gasolina", "energia eletrica",
	},
	"negocios": {
		"empresa", "companhia", "fusao", "aquisicao", "ipo", "lucro",
		"prejuizo", "receita", "resultado", "trimestre", "bolsa",
		"acoes", "ibovespa", "dividendo", "balanco", "ceo",
	},
	"startups": {
		"startup", "startups", "venture capital", "investimento anjo",
		"rodada", "serie a", "serie b", "unicornio", "seed",
		"founder", "fundador",
	},
	"tecnologia": {
		"tecnologia", "software", "aplicativo", "app", "smartphone",
		"computador", "internet", "nuvem", "chip", "semicondutor",
		"processador", "hardware", "sistema operacional", "android",
		"iphone", "whatsapp", "rede social",
	},
	"seguranca": {
		"seguranca", "ciberseguranca", "ciberataque", "hacker",
		"vazamento de dados", "vazamento", "golpe", "fraude",
		"ransomware", "phishing", "senha vazada",
	},
	"consumo": {
		"consumidor", "procon", "preco", "supermercado", "varejo",
		"loja", "compra", "assinatura", "plano de saude", "passagem aerea",
	},
	"games": {
		"game", "games", "videogame", "playstation", "xbox",
		"nintendo", "gta", "gamer", "esports", "console", "jogo eletronico",
	},
	"midia": {
		"midia", "streaming", "netflix", "filme", "serie", "musica",
		"show", "televisao", "tv", "cinema", "jornal", "podcast",
		"livro", "cultura", "festival", "novela", "artista",
	},
	"ciencia": {
		"ciencia", "pesquisa", "estudo", "nasa", "espaco", "vacina",
		"saude", "clima", "pesquisadores", "universidade", "cientistas",
		"medicina", "tratamento",
	},
}

// djCategoryTopics maps Daily Journal editorial categories onto the
// §7.2 tag list — a first-party signal that always applies.
var djCategoryTopics = map[string][]string{
	"technology":    {"tecnologia"},
	"science":       {"ciencia"},
	"economy":       {"economia"},
	"finance":       {"economia", "negocios"},
	"business":      {"negocios"},
	"politics":      {"politica"},
	"agribusiness":  {"economia", "negocios"},
	"entertainment": {"midia"},
}

// topicOrder fixes a stable tie-break so identical inputs label
// identically (map iteration is nondeterministic).
var topicOrder = []string{
	"fintech", "cripto", "regulacao", "politica", "economia",
	"negocios", "startups", "tecnologia", "IA", "seguranca",
	"consumo", "games", "midia", "ciencia",
}

// Classify scores an item's text against the topic lexicons, applies
// the kind heuristic and guesses the language. categories are the
// Daily Journal editorial categories (empty for other sources).
func Classify(title, text string, categories []string) Result {
	res := Result{Kind: "noticia", Language: DetectLanguage(title + " " + text)}
	folded := foldAccents.Replace(strings.ToLower(title + " " + text))
	titleFolded := foldAccents.Replace(strings.ToLower(title))

	scores := map[string]int{}
	matched := map[string]bool{}
	addTopics := func(tags []string) {
		for _, t := range tags {
			if !matched[t] {
				matched[t] = true
				scores[t] += 2
			}
		}
	}
	for _, c := range categories {
		addTopics(djCategoryTopics[strings.ToLower(strings.TrimSpace(c))])
	}
	for _, topic := range topicOrder {
		if topic == "IA" {
			if matchesAI(titleFolded, folded, title+" "+text) {
				scores["IA"] += 2
			}
			continue
		}
		for _, term := range topicLexicon[topic] {
			if containsTerm(titleFolded, term) {
				scores[topic] += 2
				break
			}
			if containsTerm(folded, term) {
				scores[topic]++
			}
		}
	}
	type sc struct {
		topic string
		score int
	}
	var ranked []sc
	for _, t := range topicOrder {
		if scores[t] >= 2 {
			ranked = append(ranked, sc{t, scores[t]})
		}
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	for _, r := range ranked {
		if len(res.Topics) == 5 {
			break
		}
		res.Topics = append(res.Topics, r.topic)
	}
	if len(res.Topics) == 0 {
		res.Topics = []string{"outro"}
	}
	res.Kind = classifyKind(titleFolded, categories)
	return res
}

// classifyKind applies the §7.2 kind heuristic: Daily Journal
// categories give the strongest signal (sport/entertainment are their
// own desks); otherwise title patterns decide. News is the default —
// DJ stories are reported pieces.
func classifyKind(titleFolded string, categories []string) string {
	for _, c := range categories {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "sports":
			return "esporte"
		case "entertainment":
			return "entretenimento"
		}
	}
	switch {
	case containsTerm(titleFolded, "melhores") || containsTerm(titleFolded, "melhor "),
		containsTerm(titleFolded, "guia"), containsTerm(titleFolded, "lista"),
		containsTerm(titleFolded, "onde assistir"), containsTerm(titleFolded, "como assistir"):
		return "lista"
	case containsTerm(titleFolded, "opiniao"), containsTerm(titleFolded, "analise"),
		containsTerm(titleFolded, "coluna"), containsTerm(titleFolded, "editorial"):
		return "analise"
	case containsTerm(titleFolded, "lanca"), containsTerm(titleFolded, "lancamento"),
		containsTerm(titleFolded, "estreia"), containsTerm(titleFolded, "apresenta"):
		return "lancamento"
	}
	return "noticia"
}

// matchesAI reports whether the item is about artificial intelligence.
// The folded token "ia" is a Portuguese verb form, so it only counts
// via the phrase "inteligência artificial" or a literal uppercase
// IA/AI in the original text — the same rule the topic matcher uses.
func matchesAI(titleFolded, folded, original string) bool {
	if strings.Contains(folded, "inteligencia artificial") ||
		strings.Contains(titleFolded, "inteligencia artificial") {
		return true
	}
	for _, w := range strings.FieldsFunc(original, func(r rune) bool {
		return !unicode.IsLetter(r)
	}) {
		if w == "IA" || w == "AI" {
			return true
		}
	}
	return false
}

// containsTerm reports whether the folded term appears inside the
// folded text bounded by token boundaries (start/end, space or edge).
// Multi-word terms just match their folded form literally.
func containsTerm(folded, term string) bool {
	term = strings.TrimSpace(term)
	for i := 0; ; {
		j := strings.Index(folded[i:], term)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(term)
		before := start == 0 || !isTokenChar(rune(folded[start-1]))
		after := end == len(folded) || !isTokenChar(rune(folded[end]))
		if before && after {
			return true
		}
		i = end
	}
}

func isTokenChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

var ptStops = map[string]bool{
	"de": true, "a": true, "o": true, "que": true, "e": true, "do": true,
	"da": true, "em": true, "um": true, "para": true, "com": true,
	"uma": true, "os": true, "no": true, "se": true, "na": true,
	"por": true, "mais": true, "as": true, "dos": true, "como": true,
	"mas": true, "ao": true, "das": true, "nos": true, "sao": true,
	"ele": true, "ela": true, "entre": true, "foi": true, "ser": true,
}
var enStops = map[string]bool{
	"the": true, "and": true, "to": true, "of": true, "in": true,
	"is": true, "for": true, "on": true, "that": true, "with": true,
	"are": true, "was": true, "at": true, "be": true, "by": true,
	"this": true, "from": true, "or": true, "an": true, "it": true,
	"has": true, "have": true, "will": true, "its": true,
}

// DetectLanguage guesses pt | en from stopword ratios over the first
// ~300 tokens; returns "" when the text is too short or ambiguous.
func DetectLanguage(text string) string {
	toks := Tokenize(text)
	if len(toks) > 300 {
		toks = toks[:300]
	}
	var pt, en, n int
	for _, t := range toks {
		if utf8.RuneCountInString(t) < 2 {
			continue
		}
		n++
		if ptStops[t] {
			pt++
		}
		if enStops[t] {
			en++
		}
	}
	if n < 15 {
		return ""
	}
	switch {
	case pt > en:
		return "pt"
	case en > pt:
		return "en"
	}
	return ""
}
