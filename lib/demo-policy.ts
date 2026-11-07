export function isDemoAllowed(nodeEnv = process.env.NODE_ENV): boolean {
  return nodeEnv !== "production"
}
