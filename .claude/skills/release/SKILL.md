---
name: release
description: Create a new release by tagging and pushing. Triggers GoReleaser via GitHub Actions.
argument-hint: "[version]"
---

Release workflow: tag a semver version and push it. GitHub Actions runs GoReleaser which builds binaries, creates a GitHub release, updates the Homebrew tap, and publishes deb/rpm packages.

## Steps

1. Run `git tag --sort=-v:refname | head -1` to find the latest version
2. If `$ARGUMENTS` is provided, use it as the version (prefix with `v` if missing)
3. If no argument, suggest the next patch/minor/major based on the latest tag
4. Confirm the version with the user before proceeding
5. Ensure working tree is clean (`git status --porcelain`) — abort if dirty
6. Ensure current branch is `main` — warn if not
7. Create the tag and push:
   ```bash
   git tag v<version>
   git push origin v<version>
   ```
8. Show the GitHub Actions run URL: `gh run list --workflow=release.yml --limit=1`
