# Production Release Guard

This guard prevents deploying from the wrong git baseline.

It compares:
- the commit currently running in production
- the local target commit you are about to deploy

If the target commit is **not** a descendant of the current production commit,
the script exits non-zero and refuses the deployment.

## Why this exists

This protects against a common failure mode:

1. production is already on commit `A -> B -> C`
2. a hotfix is prepared from an older branch at `A`
3. the hotfix is deployed directly
4. previously released features from `B` and `C` disappear

That is exactly the kind of silent feature rollback this guard is meant to stop.

## Usage

From the repo root:

```bash
./deploy/check_prod_descendant.sh
```

This validates local `HEAD` against the commit currently running on
`sub2api-prod`.

Validate another ref:

```bash
./deploy/check_prod_descendant.sh --target-ref codex/prod-restore-payment-antigravity
```

Only print resolved commits:

```bash
./deploy/check_prod_descendant.sh --print-only
```

## Options

```bash
./deploy/check_prod_descendant.sh \
  --target-ref <git-ref> \
  --remote-host <ssh-host> \
  --service <container-name> \
  --compose-dir <remote-compose-dir> \
  --release-dir-glob <remote-release-glob>
```

Defaults:

- `--target-ref HEAD`
- `--remote-host sub2api-prod`
- `--service sub2api`
- `--compose-dir /root/sub2api-deploy`
- `--release-dir-glob /root/sub2api-src-release-*`

## Recommended workflow

1. Run the guard before every production build:

```bash
make guard-prod-deploy
```

2. If it fails, do **not** deploy from the current branch.

3. Start a hotfix branch from the current production commit instead:

```bash
git checkout -b codex/prod-hotfix <current-production-commit>
```

4. Cherry-pick or merge only the intended changes.

5. Re-run the guard.
