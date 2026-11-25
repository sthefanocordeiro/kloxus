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

// KLOX-100 audit: the lexical floor let a sentence claim "3 de
// novembro" while the source said the 16th, and "as empresas negaram"
// while the source said "confirmaram". The two checks below close
// those gaps without a model.

func TestUnsupportedDate(t *testing.T) {
	src := "As empresas informaram que os dados foram obtidos em 16 de novembro, durante auditoria interna."
	for _, c := range []struct {
		name string
		sent string
		want bool
	}{
		{"audit: wrong pt day", "Os dados foram obtidos em 3 de novembro [1].", true},
		{"matching pt date", "Os dados foram obtidos em 16 de novembro [1].", false},
		{"no date literal", "As empresas obtiveram os dados durante uma auditoria [1].", false},
		{"wrong en date", "The data was obtained on November 3 [1].", true},
		{"matching en date", "The data was obtained on November 16 [1].", false},
		{"slash date either order", "Os dados são de 16/11 [1].", false},
		{"monthless number ignored", "Foram analisados 3 registros das empresas [1].", false},
	} {
		if got := UnsupportedDate(c.sent, src); got != c.want {
			t.Errorf("UnsupportedDate(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnsupportedDateAmbiguousSource(t *testing.T) {
	// A dd/mm source date satisfies either interpretation of an
	// ambiguous slash date in the sentence.
	src := "Documento interno datado de 03/11 detalha o caso."
	if UnsupportedDate("O caso consta de documento de 03/11 [1].", src) {
		t.Error("identical ambiguous date should pass")
	}
	if UnsupportedDate("O caso consta de documento de 11/03 [1].", src) {
		t.Error("swapped-order ambiguous date should pass against an ambiguous source")
	}
}

func TestNegationUnsupported(t *testing.T) {
	src := "As empresas confirmaram o incidente e informaram os clientes afetados."
	for _, c := range []struct {
		name string
		sent string
		want bool
	}{
		{"audit: inverted sense", "As empresas negaram qualquer incidente [1].", true},
		{"no denial cue", "As empresas confirmaram o incidente [1].", false},
		{"en denial absent", "The companies denied any incident [1].", true},
	} {
		if got := NegationUnsupported(c.sent, src); got != c.want {
			t.Errorf("NegationUnsupported(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNegationStemMatch(t *testing.T) {
	// Inflections share a stem: the sentence's "denied" accepts the
	// source's "denial".
	src := "The company issued a denial late on Friday."
	if NegationUnsupported("The company denied the incident [1].", src) {
		t.Error("denied should accept the source's denial")
	}
	srcPT := "A empresa desmentiu o incidente na sexta-feira."
	if NegationUnsupported("A empresa desmente o incidente [1].", srcPT) {
		t.Error("desmente should accept the source's desmentiu")
	}
}
