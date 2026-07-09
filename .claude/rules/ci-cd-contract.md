# CI/CD cross-repo contract

Push to `main` → `.github/workflows/build-and-deploy.yml` builds one image containing all four binaries (`api`/`batch`/`oauth`/`migrate`), pushes it to Google Artifact Registry (tags: 12-char commit SHA + `latest`), then checks out a **separate infra repo**, rewrites image tags in its manifests with `yq`, and pushes directly to its `main` (GitOps sync deploys from there). k8s manifests do NOT live in this repo; `docs/infra-repo-setup.md` is the instruction sheet for the infra-repo side.

Contracts that must not be broken silently:

- The `yq` rewrite expressions assume the infra manifests use container name `api` (API Deployment) and template name `run-batch` with a `container:` block (Argo CronWorkflow). Renaming either side breaks image updates.
- The Dockerfile's `ENTRYPOINT ["/app/api"]` + `CMD ["web", "api", "a2a"]` + `ENV ADK_LAUNCHER=prod` make the image runnable standalone; the batch Job overrides the command to `/app/batch`, the migration Job to `/app/migrate`. Keep the binary paths `/app/{api,batch,oauth,migrate}` stable.
- Adding a new binary means updating both build stages in the Dockerfile and, if it needs deployment, the infra repo.
