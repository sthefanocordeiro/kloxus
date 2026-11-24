import { SiteFooter, SiteHeader } from "../components/SiteChrome";
import { useT } from "../i18n";
import { legalFor, type LegalDocT } from "./legalText";

// /termos and /privacidade (KLOX-70): standard legal text on the same
// site chrome as the landing so the footer links resolve to real
// pages. Content lives in legalText.ts — a lazy-route module, so it
// never lands in the "/" payload (KLOX-16 budget).
function LegalDoc({ doc }: { doc: LegalDocT }) {
  const { lang } = useT();
  const t = legalFor(lang);
  return (
    <div className="min-h-dvh bg-paper text-ink">
      <SiteHeader />
      <main className="mx-auto max-w-3xl px-4 py-12 md:py-16">
        <h1 className="font-doc text-3xl font-bold md:text-4xl">{doc.title}</h1>
        <p className="mt-2 text-sm text-muted">{t.updated}</p>
        <p className="mt-6 leading-relaxed">{doc.intro}</p>
        {doc.sections.map((s, i) => (
          <section key={i} className="mt-8">
            <h2 className="font-doc text-xl font-bold">{s.h}</h2>
            {s.body.map((p, j) => (
              <p key={j} className="mt-3 text-[15px] leading-relaxed text-ink/90">
                {p}
              </p>
            ))}
          </section>
        ))}
        <p className="mt-10 border-t border-line pt-6 text-sm text-muted">
          {t.contact}{" "}
          <a href="mailto:news@kloxus-ai.trendfi.com.br" className="text-teal underline">
            news@kloxus-ai.trendfi.com.br
          </a>
        </p>
      </main>
      <SiteFooter />
    </div>
  );
}

export function TermsPage() {
  const { lang } = useT();
  return <LegalDoc doc={legalFor(lang).terms} />;
}

export function PrivacyPage() {
  const { lang } = useT();
  return <LegalDoc doc={legalFor(lang).privacy} />;
}
