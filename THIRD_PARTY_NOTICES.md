# Third-party components

The project's MIT license covers original DoOps code. Dependencies retain their own licenses.

`vendor/agent-ui/` contains the `@agent-cloud/agent-ui` 0.1.4 distribution and its original
Business Source License 1.1 in [vendor/agent-ui/LICENSE](vendor/agent-ui/LICENSE). Its authorship
and use restrictions remain unchanged. The package is kept locally so a clean checkout builds
without a private package registry. Its original archive was verified against the SHA-512
integrity value recorded in the dependency lock before extraction.

The two existing presentation adjustments in `patches/@agent-cloud__agent-ui@0.1.4.patch`
are applied to this copy: omit redundant activity titles and hide an empty execution timeline.
The package manifest removes its obsolete registry setting and points to this repository's
vendored copy. Other third-party package notices are included in their distributions.
