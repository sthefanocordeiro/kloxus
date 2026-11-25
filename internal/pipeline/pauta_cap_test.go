package pipeline

import "testing"

func TestCapPerOutlet(t *testing.T) {
	var in []PautaCard
	for i := 0; i < 8; i++ {
		in = append(in, PautaCard{StoryID: int64(i), Outlet: "Valor"})
	}
	in = append(in, PautaCard{StoryID: 100, Outlet: "InfoMoney"}, PautaCard{StoryID: 101, Outlet: "g1"})
	out := capPerOutlet(in, 10)
	count := map[string]int{}
	for _, c := range out {
		count[c.Outlet]++
	}
	if count["Valor"] > 8 || count["InfoMoney"] != 1 || count["g1"] != 1 {
		t.Fatalf("counts %v", count)
	}
	valorTop := 0
	for _, c := range out[:5] {
		if c.Outlet == "Valor" {
			valorTop++
		}
	}
	if valorTop > 3 {
		t.Fatalf("%d Valor cards in the first 5", valorTop)
	}
	if len(capPerOutlet(in, 4)) != 4 {
		t.Fatal("n not honoured")
	}
}
