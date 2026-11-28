import { redirect } from "next/navigation"

import { BLOG_LANGS } from "@/lib/blog"

// /blog has no language of its own; send readers to the default language index.
export default function BlogIndexPage() {
  redirect(`/blog/${BLOG_LANGS[0]}`)
}
