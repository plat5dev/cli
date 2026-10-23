package cmd

// Files plat5 init writes for a project without a starter (and roles.yml for any
// starter that has none). The catalogs list the services' public routes; the operator
// owns them once written (docs/routes.md).

const identityRoutesCatalog = `# Catalog of identity public routes. Apply to the control plane (or a subset).
# Not auto-published. Internal validate stays on INTERNAL_PORT.
# GET /organizations, GET /users, PATCH /users/{user_id}/status, and
# GET /members/{member_id}/user are served by identity and omitted here: they
# are for staff, through the operator gateway.
#
# Writes that manage the org carry labels. A member's role grants labels
# (docs/roles.md); its keys and sessions carry them. Reads stay unlabeled,
# except the invite list, which returns live tokens. Labels are opaque: edit
# them here, not in identity.
#
# Writes are audited by default (docs/audit.md): organization and member
# writes in the org's log, user writes in the person's own log. Creating an
# org, redeeming an invite, and minting a session are in that org's log too.
# The invite list is a read worth recording: it returns live tokens, so it
# sets audit: true.
services:
  identity:
    url: http://identity:3000
    user:
      routes:
        - path: /user
          upstream: /users/{subject.user_id}
          methods: [GET, PATCH, DELETE]
        - path: /user/memberships
          upstream: /users/{subject.user_id}/memberships
          methods: [GET]
        - path: /user/api-keys
          upstream: /users/{subject.user_id}/api-keys
          methods: [GET, POST]
        - path: /user/api-keys/{key_id}
          upstream: /users/{subject.user_id}/api-keys/{path.key_id}
          methods: [DELETE]
        - path: /user/organizations
          upstream: /users/{subject.user_id}/organizations
          methods: [POST]
        - path: /user/invites/redeem
          upstream: /users/{subject.user_id}/invites/redeem
          methods: [POST]
        - path: /user/organizations/{organization_id}/session
          upstream: /users/{subject.user_id}/organizations/{path.organization_id}/session
          methods: [POST]
    organization:
      routes:
        - path: /org
          upstream: /organizations/{subject.organization_id}
          methods:
            GET:
            PATCH:
              required_labels: [org:write]
            DELETE:
              required_labels: [org:delete]
        - path: /org/roles
          upstream: /organizations/{subject.organization_id}/roles
          methods: [GET]
        - path: /org/members
          upstream: /organizations/{subject.organization_id}/members
          methods:
            GET:
            POST:
              required_labels: [org:members:write]
        - path: /org/members/{member_id}
          upstream: /organizations/{subject.organization_id}/members/{path.member_id}
          methods:
            GET:
            PATCH:
              required_labels: [org:members:write]
            DELETE:
              required_labels: [org:members:write]
        - path: /org/invites
          upstream: /organizations/{subject.organization_id}/invites
          methods: [GET, POST]
          required_labels: [org:members:write]
          audit: true
        - path: /org/invites/{invite_id}
          upstream: /organizations/{subject.organization_id}/invites/{path.invite_id}
          methods: [DELETE]
          required_labels: [org:members:write]
        - path: /org/service-accounts
          upstream: /organizations/{subject.organization_id}/service-accounts
          methods:
            GET:
            POST:
              required_labels: [org:service-accounts:write]
        - path: /org/service-accounts/{service_account_id}
          upstream: /organizations/{subject.organization_id}/service-accounts/{path.service_account_id}
          methods:
            GET:
            PATCH:
              required_labels: [org:service-accounts:write]
            DELETE:
              required_labels: [org:service-accounts:write]
        - path: /org/service-accounts/{service_account_id}/api-keys
          upstream: /organizations/{subject.organization_id}/service-accounts/{path.service_account_id}/api-keys
          methods:
            GET:
            POST:
              required_labels: [org:service-accounts:write]
        - path: /org/service-accounts/{service_account_id}/api-keys/{key_id}
          upstream: /organizations/{subject.organization_id}/service-accounts/{path.service_account_id}/api-keys/{path.key_id}
          methods: [DELETE]
          required_labels: [org:service-accounts:write]
    member:
      routes:
        - path: /member
          upstream: /members/{subject.member_id}
          methods: [GET, PATCH, DELETE]
        - path: /member/api-keys
          upstream: /members/{subject.member_id}/api-keys
          methods: [GET, POST]
        - path: /member/api-keys/{key_id}
          upstream: /members/{subject.member_id}/api-keys/{path.key_id}
          methods: [DELETE]
`

const auditRoutesCatalog = `# Catalog of audit public routes. Apply to the control plane.
# Not auto-published. Internal intent/outcome writes stay on INTERNAL_PORT.
# Skip this file when audit is off (AUDIT_ENABLED=false on the gateway).
#
# An org's log carries IPs and user agents of every member, so reading it is
# labeled. A person reads their own log. Labels are opaque: edit them here,
# not in audit.
services:
  audit:
    url: http://audit:3002
    user:
      routes:
        - path: /user/audit-events
          upstream: /users/{subject.user_id}/audit-events
          methods: [GET]
    organization:
      routes:
        - path: /org/audit-events
          upstream: /organizations/{subject.organization_id}/audit-events
          methods: [GET]
          required_labels: [org:audit:read]
`

// starterRoles is a starting point, not Plat5's: Plat5 names no roles (docs/roles.md).
const starterRoles = `# Your roles. Each grants labels; routes require them (required_labels).
# ["*"] grants every label. The org:* labels match routes.identity.yml and
# routes.audit.yml. Add your own services' labels here. plat5 start writes this
# file to the control plane; identity picks it up.
roles:
  owner: ["*"]
  admin: [org:write, org:members:write, org:service-accounts:write, org:audit:read]
  member: []
creator_role: owner
default_role: member
`

const sampleRoutes = `services:
  api:
    # url comes from plat5.yml upstreams (applied by the CLI)
    user:
      routes:
        - path: /user/widgets
          upstream: /users/{subject.user_id}/widgets
          methods: [GET, POST]
        - path: /user/widgets/{id}
          upstream: /users/{subject.user_id}/widgets/{path.id}
          methods: [GET, PATCH, DELETE]
`
