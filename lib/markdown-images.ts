import { createElement, type ReactElement } from "react"

// DoSql and DoWeb answers can repeat text that someone outside the console
// controls, such as a database row or a page the Agent read. An <img> in that
// answer is fetched as soon as it renders (React also adds a preload link), so
// a crafted answer could send query results to any host without a click. Those
// pages pass imageAsText to the Markdown component: the image is shown as
// plain text and nothing is requested.
export function imageAsText({ src, alt }: { src?: unknown; alt?: unknown }): ReactElement {
  const url = typeof src === "string" ? src.trim() : ""
  const label = typeof alt === "string" && alt.trim() ? `图片：${alt.trim()}` : "图片"
  return createElement(
    "span",
    { className: "break-all text-muted-foreground" },
    url ? `[${label}，未加载 ${url}]` : `[${label}，未加载]`,
  )
}
