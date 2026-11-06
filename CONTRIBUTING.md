# Contributing

## Pull requests

### Pull request body

Write the pull request body using the repository's
[pull request template](.github/pull_request_template.md). Follow all instructions
in the template, including its language, length, and verification requirements.

### Pull request title

Write the pull request title in the Conventional Commits format:

```text
<type>[optional scope]: <description>
```

Use one of the types accepted by the
[Semantic PR check](.github/workflows/00-semantic-pr-check.yaml): `build`, `chore`,
`ci`, `docs`, `feat`, `fix`, `perf`, `refactor`, `revert`, `style`, or `test`.
The description must not end with a period.

Examples:

```text
docs: add pull request contribution guidelines
fix(actions): validate pull request titles
feat: driftline の配布対象に CONTRIBUTING.md を追加
```
