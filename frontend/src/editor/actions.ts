import { createContext } from "react";

// ComposeInsert pre-fills the in-document composer: a fact or source
// excerpt plus that source's citation ref. seq lets the same insert
// land twice.
export interface ComposeInsert {
  text: string;
  cite: number;
  seq: number;
}

// ComposeDraft is the composer's in-progress input. The document
// rebuilds on every version bump (commentary autosave included), which
// remounts every node view — the page keeps the draft so a save landing
// mid-sentence cannot erase it (KLOX-96).
export interface ComposeDraft {
  text: string;
  cites: number[];
  myTake: boolean;
}

// What the sentence/commentary node views can ask the issue page to do.
export interface DocActions {
  // returns null on success, or the refusal reason to show inline —
  // the edit box stays open with the typed text either way
  editSentence: (sid: string, text: string, citations: number[]) => Promise<string | null>;
  interpret: (sid: string, text?: string) => Promise<boolean>;
  removeSentence: (sid: string) => Promise<boolean>;
  // bulk action: drop every flagged sentence of an item in one call
  removeFlagged: (itemRef: number) => Promise<boolean>;
  // open the evidence pane on item ref, optionally highlighting a
  // citation's passage and flashing the sentence
  showEvidence: (itemRef: number, opts?: { citeRef?: number; sid?: string; issueItemId?: number }) => void;
  // manual compose: append editor-written text to an item — returns
  // null on success or the refusal reason to show inline
  addSentence: (itemRef: number, text: string, citations: number[], kind: "fact" | "interpretation") => Promise<string | null>;
  // composer blocks (KLOX-66): index is the block's position inside the
  // section's block list; updateBlock returns the refusal like
  // editSentence, the others report plain success. insertBlock takes
  // the gap position — blocks insert anywhere now (D3).
  moveBlock: (section: string, index: number, dir: -1 | 1) => Promise<boolean>;
  removeBlock: (section: string, index: number) => Promise<boolean>;
  updateBlock: (section: string, index: number, text: string, url?: string) => Promise<string | null>;
  insertBlock: (section: string, kind: "heading" | "divider" | "link" | "image" | "embed" | "text",
    position?: number, text?: string, url?: string) => Promise<string | null>;
  // opens the page-owned insert dialogs — image upload/URL, embed HTML
  // and the standard-message library (D3)
  openInsert: (section: string, index: number, kind: "image" | "embed" | "message") => void;
  // uploads one image file (base64) → returns the /m/ URL, null on failure
  uploadImage: (dataB64: string, contentType: string, name: string) => Promise<string | null>;
  // opens the pauta picker for the section — the page owns the dialog;
  // position is the gap the picker was opened from (append when unset)
  pickPauta: (section: string, position?: number) => void;
  // link-only toggle (KLOX-66 D2): the pauta ships its link with no
  // written text — nothing to write or cite for it
  toggleLinkOnly: (ref: number, linkOnly: boolean) => Promise<boolean>;
  // in-document composer (KLOX-96): the pauta heading owns the writing
  // surface — composeFor opens it under item `ref` (optionally pre-
  // filled), composeClose dismisses it, composeRef is the open item.
  // One composer at a time: opening another pauta replaces the open one
  // (one-pauta focus).
  composeFor: (ref: number, insert?: ComposeInsert) => void;
  composeClose: (ref: number) => void;
  composeRef: number | null;
  // the last prefill request — the matching item heading seeds its
  // composer from it; seq distinguishes repeat inserts of the same fact
  composeInsert: { ref: number; insert: ComposeInsert | null; seq: number } | null;
  // consumeInsert clears the delivered prefill — one-shot semantics, so
  // a node-view remount after a refetch cannot re-seed a fact the
  // creator already used
  consumeInsert: (ref: number) => void;
  // draft parking across node-view remounts: take reads and clears the
  // parked draft on composer mount, save stores the latest on change
  composeDraftTake: (ref: number) => ComposeDraft | null;
  composeDraftSave: (ref: number, d: ComposeDraft) => void;
  readOnly: boolean;
}

export const DocActionsCtx = createContext<DocActions>({
  editSentence: async () => null,
  interpret: async () => false,
  removeSentence: async () => false,
  removeFlagged: async () => false,
  showEvidence: () => {},
  addSentence: async () => null,
  moveBlock: async () => false,
  removeBlock: async () => false,
  updateBlock: async () => null,
  insertBlock: async () => null,
  openInsert: () => {},
  uploadImage: async () => null,
  pickPauta: () => {},
  toggleLinkOnly: async () => false,
  composeFor: () => {},
  composeClose: () => {},
  composeRef: null,
  composeInsert: null,
  consumeInsert: () => {},
  composeDraftTake: () => null,
  composeDraftSave: () => {},
  readOnly: true,
});
