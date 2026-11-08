async function languageExtension(language) {
  switch (language) {
    case "javascript":
      return (await import("@codemirror/lang-javascript")).javascript();
    case "jsx":
      return (await import("@codemirror/lang-javascript")).javascript({
        jsx: true
      });
    case "typescript":
      return (await import("@codemirror/lang-javascript")).javascript({
        typescript: true
      });
    case "tsx":
      return (await import("@codemirror/lang-javascript")).javascript({
        jsx: true,
        typescript: true
      });
    case "json":
      return (await import("@codemirror/lang-json")).json();
    case "html":
      return (await import("@codemirror/lang-html")).html();
    case "css":
      return (await import("@codemirror/lang-css")).css();
    case "markdown":
      return (await import("@codemirror/lang-markdown")).markdown();
    case "python":
      return (await import("@codemirror/lang-python")).python();
    case "go":
      return (await import("@codemirror/lang-go")).go();
    case "yaml":
      return (await import("@codemirror/lang-yaml")).yaml();
    case "shell": {
      const [{ StreamLanguage }, { shell }] = await Promise.all([
        import("@codemirror/language"),
        import("@codemirror/legacy-modes/mode/shell")
      ]);
      return StreamLanguage.define(shell);
    }
    default:
      return [];
  }
}
export {
  languageExtension as l
};
