package store

// Non-editorial feed detection (owner decision, KLOX-55): release
// lists, where-to-watch match guides and outlet self-promotion reach
// the board even though they never match a newsletter's editorial
// line. Under llm_mode=off there is no triage model to weigh them,
// so deterministic ranking demotes matching stories below real
// coverage. Matching uses the same folded text as promo detection.

import "strings"

// watchTerms mark where/what/how-to-watch listings — match-day guides
// and streaming availability, not reported stories.
var watchTerms = []string{
	"onde assistir", "o que assistir", "como assistir",
	"where to watch", "how to watch", "what to watch", "where to stream",
}

// releaseListTerms mark release calendars: streaming catalogs,
// cinema/game/device launch lists. A launch announcement reporting
// one event does not match — the terms need list-shaped phrasing.
var releaseListTerms = []string{
	"estreias da semana", "estreias do mes", "estreias de",
	"o que chega", "o que estreia", "tudo que chega",
	"lancamentos da semana", "lancamentos do mes",
	"novidades da semana", "novidades do mes", "guia de estreias",
	"what's coming to", "coming to netflix", "new on netflix",
	"new this month on", "release schedule",
}

// strongSupporter is self-referential on its own — "seja sócio",
// "become a member", "apoie-nos" all mean the outlet itself. The
// Portuguese imperative "doe" is a common substring (adoece, condoe)
// and stays out even here.
var strongSupporter = []string{
	"seja socio", "seja membro", "seja apoiador", "apoie-nos", "ajude-nos",
	"programa de membros", "clube de membros",
	"support us", "become a member", "become a supporter",
	"become a subscriber", "join our community",
}

// weakSupporter is a funding ask that only counts as self-promo when
// the title names the outlet or a media noun — "apoie o jornalismo" is
// the outlet campaigning for itself; "apoie a escola" is a cause.
var weakSupporter = []string{
	"apoie", "ajude", "apoiador", "apoiadores", "contribua",
	"vaquinha", "financiamento coletivo", "crowdfunding", "donate",
}

// mediaNouns tie the ask to the outlet's own operation — "apoie o
// jornalismo independente" is self-promo; the same verb for an outside
// cause is not. Naming the source in the title counts too.
var mediaNouns = []string{
	"jornalismo", "jornalistico", "redacao", "newsletter", "conteudo",
	"podcast", "veiculo", "site", "canal", "trabalho", "plataforma",
	"journalism", "newsroom", "reporting", "content", "channel",
	"our work", "our journalism", "our coverage",
}

// IsNonEditorialTitle reports whether a feed title is obviously outside
// a newsletter's editorial line: a release list, a where-to-watch guide
// or the outlet promoting itself. `outlet` is the item's source name —
// a funding ask that names it is self-promo even without media nouns.
func IsNonEditorialTitle(title, outlet string) bool {
	t := foldDealText(title)
	for _, term := range watchTerms {
		if strings.Contains(t, term) {
			return true
		}
	}
	for _, term := range releaseListTerms {
		if strings.Contains(t, term) {
			return true
		}
	}
	for _, term := range strongSupporter {
		if strings.Contains(t, term) {
			return true
		}
	}
	ask := false
	for _, term := range weakSupporter {
		if strings.Contains(t, term) {
			ask = true
			break
		}
	}
	if !ask {
		return false
	}
	if outlet != "" && strings.Contains(t, foldDealText(outlet)) {
		return true
	}
	for _, noun := range mediaNouns {
		if strings.Contains(t, noun) {
			return true
		}
	}
	return false
}
