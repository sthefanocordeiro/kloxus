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
	folded := foldText(part)
	for _, label := range NicheLabels {
		if folded == foldText(label) {
			return true
		}
	}
	return false
}
