// Demo mode is on in development, and can be enabled for a production build
// with NEXT_PUBLIC_DOOPS_DEMO=1 (preview/staging deployments). It is never on
// for an ordinary production build.
export function isDemoAllowed(nodeEnv = process.env.NODE_ENV): boolean {
  if (nodeEnv !== "production") return true
  return process.env.NEXT_PUBLIC_DOOPS_DEMO === "1"
}
