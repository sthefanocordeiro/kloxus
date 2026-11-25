import { describe, expect, it } from "vitest";
import {
  absolutizeHtml, charCount, foldIndex, htmlToPlain, splitForX, standaloneHtml, xCount, X_LIMIT,
} from "../lib/renditions";

describe("renditions helpers", () => {
  it("counts code points and keeps line breaks", () => {
    expect(charCount("a\n\nb")).toBe(4);
    expect(charCount("😀ção")).toBe(4);
    expect(charCount("a\r\nb")).toBe(3);
  });

  it("charges 23 per link on X", () => {
    const url = "https://example.com/" + "x".repeat(100);
    expect(xCount(`oi ${url}`)).toBe(3 + 23);
  });

  it("finds the ver-mais fold at a word boundary", () => {
    expect(foldIndex("curto")).toBe(-1);
    const text = ("palavra ".repeat(60)).trim();
    const i = foldIndex(text);
    expect(i).toBeGreaterThan(150);
    expect(i).toBeLessThanOrEqual(210);
    expect(text[i]).toBe(" ");
  });

  it("splits a long text into numbered posts within the limit", () => {
    const para = "Uma frase razoavelmente longa para encher espaço no post. ".repeat(12).trim();
    const posts = splitForX(`${para}\n\n${para}`);
    expect(posts.length).toBeGreaterThan(2);
    posts.forEach((p, i) => {
      expect(xCount(p)).toBeLessThanOrEqual(X_LIMIT);
      expect(p.endsWith(` ${i + 1}/${posts.length}`)).toBe(true);
    });
    expect(splitForX("curto")).toEqual(["curto"]);
  });

  it("keeps every blank-line block its own post — opening first, one per story", () => {
    const posts = splitForX(
      "Abertura do fio.\n\nNotícia um\nhttps://a.example/um\nTexto um.\n\nNotícia dois\nhttps://b.example/dois\nTexto dois.",
    );
    expect(posts).toEqual([
      "Abertura do fio. 1/3",
      "Notícia um\nhttps://a.example/um\nTexto um. 2/3",
      "Notícia dois\nhttps://b.example/dois\nTexto dois. 3/3",
    ]);
    expect(posts.join(" ")).not.toContain("---");
  });

  it("treats a '---' block as a boundary, never post text", () => {
    const posts = splitForX("Abertura.\n\n---\n\nNotícia\nhttps://a.example/1\nTexto.");
    expect(posts).toEqual(["Abertura. 1/2", "Notícia\nhttps://a.example/1\nTexto. 2/2"]);
  });

  it("absolutizes root-relative media and links only", () => {
    const html = '<img src="/m/1-ab"><a href="https://x.com/a">x</a><a href="//cdn/x">y</a>';
    expect(absolutizeHtml(html, "https://kloxus.fly.dev/")).toBe(
      '<img src="https://kloxus.fly.dev/m/1-ab"><a href="https://x.com/a">x</a><a href="//cdn/x">y</a>',
    );
  });

  it("wraps a standalone file and derives plain text", () => {
    expect(standaloneHtml("A & B", "<p>x</p>")).toContain("<title>A &amp; B</title>");
    expect(htmlToPlain("<h1>T</h1><p>um</p><p>dois</p>")).toBe("T\ndum\ndois".replace("T\nd", "T\n"));
  });
});
