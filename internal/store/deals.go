package store

// Deals/coupons/buying-guide detection (owner decision, KLOX-45).
// promoTerms is the single term list for the whole product: ingest marks
// matching items kind='promo', and promo rows are stored but never enter
// a run's candidate set (dedupe, triage, board). Matching is case- and
// accent-insensitive substring match on the whitespace-normalized title.
// The migration backfill duplicates the folded list in SQL — keep them
// in sync when adding terms.
import "strings"

var promoTerms = []string{
	"promocao", "promocoes", "oferta", "desconto", "cupom", "% off",
	"black friday", "black november", "black da", "esquenta black",
	"11.11", "cyber monday", "menor preco", "preco historico",
	"selecionados", "veja uma selecao", "em ate 12x",
	"na amazon", "no magalu", "no mercado livre",
	"aliexpress", "achados do",
}

// dealFold strips the Portuguese accents the term list ignores; lower()
// runs before it so only lowercase accented forms are needed.
var dealFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

func foldDealText(s string) string {
	return strings.Join(strings.Fields(dealFold.Replace(strings.ToLower(s))), " ")
}

// IsPromoTitle reports whether a title looks like deals, coupons or
// buying-guide content rather than news.
func IsPromoTitle(title string) bool {
	t := foldDealText(title)
	for _, term := range promoTerms {
		if strings.Contains(t, term) {
			return true
		}
	}
	return false
}
