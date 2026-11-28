# Plat5 CLI docs

`docs/` is law for the CLI's composable project scaffolding, registries, and local stack contract. Concepts hold the rules, reference holds the exact file shapes and resolution behavior. When two docs disagree, the concept doc wins.

For command usage and local stack quickstart, see [`../README.md`](../README.md).

## Start here, by what you're doing

| You're | Read |
|--------|------|
| Understanding how `plat5 init` and `plat5 add` compose layers and services | [composition](concepts/composition.md) |
| Authoring a plugin (service/DDD slice, IdP, AuthZEN PDP, console, observability, CI) | [plugin manifest](reference/plugin.md) |
| Publishing or adding a first-party or 3rd-party registry | [registry](reference/registry.md) |
| Configuring local auth, operator SSO, observability, or `.env.local` | [local config](reference/local-config.md) |
| Implementing the phased rollout | [roadmap](roadmap.md) |

## Concepts

| Doc | What it decides |
|-----|-----------------|
| [composition](concepts/composition.md) | `type` vs. `slot` vs. Plat5 `kinds`, multi-service DDD slices, managed core vs. project-owned components, assembly pipeline, secret hygiene |

## Reference

| Doc | Contents |
|-----|----------|
| [plugin](reference/plugin.md) | `plat5.plugin.yml`: plugin types, compatibility rules, prompts, verify probes, `[[ ]]` rendering, fragments |
| [registry](reference/registry.md) | `registry.yml`: version matching, reference syntax, caching, private repos, `plat5 registry` commands |
| [local-config](reference/local-config.md) | `plat5.local.yml` extensions (`idp.client_id`, `operator.idp`, `observability`, `otel.headers`), `.env.local`, auto-networking |

## Implementation

| Doc | Contents |
|-----|----------|
| [roadmap](roadmap.md) | Phased delivery plan across `cli/` and `../registry` |
