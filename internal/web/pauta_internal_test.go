package web

import (
	"reflect"
	"testing"
)

func TestPautaLines(t *testing.T) {
	got := pautaLines("# Titulo\r\n\n> citação [1] aqui\n  - item <!-- x -->\n")
	want := []string{"Titulo", "citação (1) aqui", "item  x -->"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pautaLines = %q, want %q", got, want)
	}
	if len(pautaLines("  \n\n")) != 0 {
		t.Fatal("blank text must yield no lines")
	}
}
