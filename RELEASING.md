# Releasing

End-to-end release lifecycle for `neo4j-mcp-canary`. Most of it is automated — your job as a contributor is one changelog entry per PR; everything downstream happens on merge.


## Step 1 — Add a changie entries for your work

User-facing changes (new features, bug fixes, behavior changes visible to CLI users) need a changelog entry. Internal-only changes (CI, refactors, build tooling with no user impact) don't.

pick a kind (`Major` / `Minor` / `Patch`) and a body. Commit the resulting YAML in `.changes/unreleased/` alongside your code.
```bash
changie new --kind Patch --body "fix instance list pagination"
```

## Step 2 — Raise PR, fix, and merge 

1. Commit,push and then raise a PR. 
2. Wait for the actions to complete. Any fixes to address errors will need changie entries as in Step 1.  Make sure these are committed and pushed.
3. When all errors are resolved, merge the PR



## Step 3 - Release

1. Make sure **main** is up to date with your merged PR
```bash
git checkout main && git pull origin main --ff-only && git log --oneline -3
```
2. On main bring together the changie entries with `changie batch` with the type of release this is e.g patch.  This then folds `.changes/unreleased/*.yaml` into `.changes/v<version>.md`.

2. Create the changelog 
```bash
changie merge
```

Make a note of the release version. This is the latest file in `.changes` e.g __v0.1.1.md__

3. Commit and push

```bash
git add .
git commit -m "Chore: release <release version from Step 2"
git push
```

4. Using the release version, create the release tag
```bash
git tag <version from .changes>
```

5. Push the tag
```bash
 git push origin <tag from Step 4>
```

6. The release action should be triggered
