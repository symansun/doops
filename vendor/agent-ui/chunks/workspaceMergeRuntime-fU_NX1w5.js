import { defaultKeymap } from "@codemirror/commands";
import { foldGutter, syntaxHighlighting, defaultHighlightStyle } from "@codemirror/language";
import { unifiedMergeView, MergeView } from "@codemirror/merge";
import { EditorState } from "@codemirror/state";
import { EditorView, lineNumbers, drawSelection, keymap } from "@codemirror/view";
import { l as languageExtension } from "./workspaceCodeLanguageLoad-CGoLytXV.js";
async function mountMergeDiff(input) {
  const language = input.highlight ? await languageExtension(input.language) : [];
  const shared = readonlyExtensions(language);
  if (input.unified) {
    const view = new EditorView({
      parent: input.parent,
      state: EditorState.create({
        doc: input.after,
        extensions: [
          ...shared,
          unifiedMergeView({
            original: input.before,
            mergeControls: false,
            collapseUnchanged: { margin: 3, minSize: 4 }
          })
        ]
      })
    });
    return { destroy: () => view.destroy() };
  }
  const merge = new MergeView({
    parent: input.parent,
    a: { doc: input.before, extensions: shared },
    b: { doc: input.after, extensions: shared },
    collapseUnchanged: { margin: 3, minSize: 4 }
  });
  return { destroy: () => merge.destroy() };
}
function readonlyExtensions(language) {
  return [
    EditorState.readOnly.of(true),
    EditorView.editable.of(false),
    lineNumbers(),
    foldGutter(),
    drawSelection(),
    syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
    keymap.of(defaultKeymap),
    language,
    EditorView.theme({
      "&": { height: "100%" },
      ".cm-scroller": { overflow: "auto" },
      ".cm-mergeView": { height: "100%", overflow: "auto" }
    })
  ];
}
export {
  mountMergeDiff
};
