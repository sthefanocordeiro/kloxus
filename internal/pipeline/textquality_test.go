package pipeline

import "testing"

func TestCitationNums(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []int
	}{
		{"fato [1].", []int{1}},
		{"fato [1][3] e [12].", []int{1, 3, 12}},
		{"sem citação.", nil},
		{"[10] no início.", []int{10}},
	} {
		got := CitationNums(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("CitationNums(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("CitationNums(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

func TestSupportedBySource(t *testing.T) {
	src := "O regulador do trabalho abriu consulta pública sobre o uso de sistemas automatizados em processos de contratação. Comentários podem ser enviados até 15 de dezembro pelo portal do órgão."
	for _, c := range []struct {
		name string
		sent string
		want bool
	}{
		{"full lexicon support", "A consulta pública do regulador do trabalho trata de sistemas automatizados em contratação", true},
		{"citations stripped", "Comentários podem ser enviados até 15 de dezembro [1]", true},
		{"unrelated claim", "A seleção da Valdória venceu o torneio interligas de handebol sub-20", false},
		{"empty of content words", "E aí, é?", true},
	} {
		if got := SupportedBySource(c.sent, src); got != c.want {
			t.Errorf("SupportedBySource(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSupportedBySourceWeightedFloor(t *testing.T) {
	src := "O regulador abriu consulta pública sobre inteligência artificial em recrutamento."
	// Half the content words absent — below the 60% floor even though
	// some lexicon is shared.
	if SupportedBySource("O regulador fechou o cinema municipal de Valdória nesta segunda", src) {
		t.Error("weakly-overlapping sentence passed the lexical floor")
	}
	// Numbers and proper nouns count double: matching "15" plus the
	// capitalized "Valdória" carries more weight than shared glue.
	src2 := "A medida vale a partir de 15 de janeiro."
	if !SupportedBySource("A medida vale a partir de 15 de janeiro para todos [1].", src2) {
		t.Error("verbatim-supported sentence failed the lexical floor")
	}
}

func TestTooCloseToSource(t *testing.T) {
	src := "O regulador do trabalho abriu consulta pública sobre o uso de sistemas automatizados em processos de contratação e seleção de pessoal."
	for _, c := range []struct {
		name string
		sent string
		want bool
	}{
		{"10-word run", "Ontem o regulador do trabalho abriu consulta pública sobre o uso de sistemas automatizados ontem", true},
		{"9-word run", "Bem, o regulador do trabalho abriu consulta pública sobre o rumo novo", false},
		{"paraphrase", "O órgão regulador iniciou uma consulta pública sobre contratação automatizada", false},
		{"short attributed quote",
			`Segundo a agência, "o regulador do trabalho abriu consulta pública sobre o uso de sistemas" na terça`, false},
		{"overlong quote is not a quotation",
			`Disse ainda: "o regulador do trabalho abriu consulta pública sobre o uso de sistemas automatizados em processos de contratação e seleção de pessoal nesta semana inteira sem pausa"`, true},
	} {
		if got := TooCloseToSource(c.sent, src); got != c.want {
			t.Errorf("TooCloseToSource(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHasNumberDateOrQuote(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"A consulta termina em 15 de dezembro.", true},
		{"Metade das empresas usa IA.", false},
		{"Cerca de 40% do tráfego é bot.", true},
		{"A votação é na terça-feira.", true},
		{"The vote is on Tuesday.", true},
		{`Disse: "não vamos recuar".`, true},
		{"O debate segue aberto no setor.", false},
	} {
		if got := HasNumberDateOrQuote(c.in); got != c.want {
			t.Errorf("HasNumberDateOrQuote(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
