import { defaultKeymap } from "@codemirror/commands";
import { foldGutter, syntaxHighlighting, defaultHighlightStyle } from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap, openSearchPanel } from "@codemirror/search";
import { Compartment, EditorState, EditorSelection } from "@codemirror/state";
import { EditorView, lineNumbers, drawSelection, keymap } from "@codemirror/view";
import { l as languageExtension } from "./workspaceCodeLanguageLoad-CGoLytXV.js";
async function mountReadonlyEditor(input) {
  const languageCompartment = new Compartment();
  const view = new EditorView({
    parent: input.parent,
    state: EditorState.create({
      doc: input.doc,
      extensions: [
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
        lineNumbers(),
        foldGutter(),
        drawSelection(),
        highlightSelectionMatches(),
        syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
        keymap.of([...searchKeymap, ...defaultKeymap]),
        EditorView.theme({
          "&": { height: "100%" },
          ".cm-scroller": { overflow: "auto" }
        }),
        languageCompartment.of(
          input.highlight ? await languageExtension(input.language) : []
        )
      ]
    })
  });
  return {
    view,
    setDoc(next) {
      replaceDocPreservingView(view, next);
    },
    async setLanguage(language, highlight) {
      const extension = highlight ? await languageExtension(language) : [];
      view.dispatch({
        effects: languageCompartment.reconfigure(extension)
      });
    },
    openFind() {
      openSearchPanel(view);
    },
    revealLine(line) {
      const clamped = Math.min(
        Math.max(1, line),
        view.state.doc.lines
      );
      const pos = view.state.doc.line(clamped).from;
      view.dispatch({
        selection: EditorSelection.cursor(pos),
        scrollIntoView: true
      });
    },
    selection() {
      const range = view.state.selection.main;
      if (range.from === range.to) return null;
      return {
        from: range.from,
        to: range.to,
        text: view.state.sliceDoc(range.from, range.to)
      };
    },
    destroy() {
      view.destroy();
    }
  };
}
function replaceDocPreservingView(view, next) {
  if (view.state.doc.toString() === next) return;
  const length = next.length;
  const ranges = view.state.selection.ranges.map(
    (range) => EditorSelection.range(
      Math.min(range.anchor, length),
      Math.min(range.head, length)
    )
  );
  view.dispatch({
    changes: { from: 0, to: view.state.doc.length, insert: next },
    selection: EditorSelection.create(ranges, view.state.selection.mainIndex),
    effects: view.scrollSnapshot(),
    scrollIntoView: false
  });
}
export {
  mountReadonlyEditor
};
