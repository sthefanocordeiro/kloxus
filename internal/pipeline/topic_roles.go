package pipeline

// Editoria/assunto recovery for the onboarding topic encoding. The
// cadastro stores one comma-separated publications.topic containing the
// creator's typed subjects plus the labels of the editoria chips they
// picked. Ranking must not treat that flat string as one role-less bag:
// publisher categories are evidence of an editoria, while typed subjects
// need independent evidence in the story (KLOX-278).

import (
	"sort"
	"strings"

	"github.com/sthefanocordeiro/kloxus/internal/store"
)

// NicheLabels is the canonical editoria key -> display label map shared
// by onboarding and ranking. Keep it in pipeline because ranking is the
// consumer that must recover the two roles from the stored topic.
var NicheLabels = map[string]string{
	"politica": "política", "economia": "economia", "mercado-financeiro": "mercado financeiro e investimentos",
	"financas-pessoais": "finanças pessoais", "negocios": "negócios e empresas", "startups": "startups e inovação",
	"carreira": "carreira e trabalho", "tecnologia": "tecnologia", "agronegocio": "agronegócio",
	"internacional": "internacional", "brasil-cidades": "Brasil e cidades", "justica-direito": "justiça e direito",
	"saude": "saúde", "educacao": "educação", "ciencia": "ciência", "meio-ambiente": "meio ambiente e sustentabilidade",
	"cultura": "cultura e entretenimento", "estilo-vida": "estilo de vida e consumo",
}

// topicRoles recovers the two ranking roles for a publication. Rows loaded
// from Postgres carry explicit editoria state in Niches: that state is
// authoritative, even when empty, so creator prose can never select or
// resurrect a chip by mentioning its label (KLOX-274). A nil Niches slice
// means a hand-built pre-KLOX-274 publication in a unit test; only that
// legacy shape falls back to recovering roles from the flat topic.
func topicRoles(pub *store.Publication) (editorias, subjects string) {
	if pub == nil {
		return "", ""
	}
	if pub.Niches == nil {
		return splitTopicRoles(pub.Topic)
	}
	selected := map[string]bool{}
	for _, key := range pub.Niches {
		if _, ok := NicheLabels[key]; ok {
			selected[key] = true
		}
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var ed, subj []string
	for _, key := range keys {
		ed = append(ed, NicheLabels[key])
	}
	for _, raw := range strings.FieldsFunc(pub.Topic, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if key := nicheKeyForExactLabel(part); key != "" && selected[key] {
			continue
		}
		subj = append(subj, part)
	}
	return strings.Join(ed, ", "), strings.Join(subj, ", ")
}

func nicheKeyForExactLabel(part string) string {
	folded := foldText(part)
	for key, label := range NicheLabels {
		if folded == foldText(label) {
			return key
		}
	}
	return ""
}

// splitTopicRoles recovers editoria labels and typed subjects from the
// flat topic encoding. Editorias are recognized case/accent-insensitively
// even when the label occurs inside the creator's text, because the
// onboarding folder intentionally does not append a duplicate label when
// the text already contains it. Subjects are the comma/semicolon/pipe
// parts that are not exactly an editoria label; a label embedded in a
// larger part remains the creator's own subject text.
func splitTopicRoles(topic string) (editorias, subjects string) {
	foldedTopic := foldText(topic)
	var ed, subj []string
	seenEd := map[string]bool{}
	keys := make([]string, 0, len(NicheLabels))
	for key := range NicheLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		label := NicheLabels[key]
		folded := foldText(label)
		if folded != "" && strings.Contains(foldedTopic, folded) && !seenEd[folded] {
			seenEd[folded] = true
			ed = append(ed, label)
		}
	}
	for _, raw := range strings.FieldsFunc(topic, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		part := strings.TrimSpace(raw)
		if part == "" {
			continue
		}
		if isNicheLabelPart(part) {
			continue
		}
		subj = append(subj, part)
	}
	return strings.Join(ed, ", "), strings.Join(subj, ", ")
}

func isNicheLabelPart(part string) bool {
	return nicheKeyForExactLabel(part) != ""
}
