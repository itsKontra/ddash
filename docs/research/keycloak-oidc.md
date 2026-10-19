# Keycloak OIDC for a Go backend serving an embedded SPA

Research for [#6](https://github.com/itsKontra/ddash/issues/6), part of the [ddash v1 spec map](https://github.com/itsKontra/ddash/issues/1). Sources were checked on 2026-10-19.

**Question.** When the Go binary serves the React SPA, which OIDC flow should ddash use: a backend-for-frontend (BFF) with a server session cookie, or tokens held in the SPA? Which Keycloak client settings does it need? What changes if ddash is later exposed through the Cloudflare Tunnel with Cloudflare Access in front?

## Answer in brief

- **Use a BFF.** The Go binary is a confidential OIDC client that runs the Authorization Code flow with PKCE, verifies the ID token, and gives the browser only an opaque, HttpOnly session cookie. The SPA never holds a token. The IETF BCP ranks this as the most secure of its three browser-app architectures [1].
- **Keycloak client.** Client authentication **On** (confidential). Only **Standard flow** enabled. PKCE method **S256**. Exact redirect URI(s) with no wildcard. A matching post-logout redirect URI. Web origins left empty [3].
- **Cookie and CSRF.** Set the cookie `Secure; HttpOnly; Path=/`, with no `Domain` and a `__Host-` prefix. Use `SameSite=Strict` for the session cookie and `SameSite=Lax` for the short-lived login-state cookie. Require a custom header on every state-changing API call [1].
- **Behind Cloudflare Access.** Nothing in the OIDC design changes. Add a second, outer gate: Keycloak as Access's OIDC identity provider, and JWT validation at the origin (or `cloudflared`'s `access.required`). Keycloak's public hostname must be fixed so the issuer is the same from the LAN and the internet. Same-origin serving avoids every Access CORS pitfall [6][7][8][9][10].

## 1. Flow: BFF vs tokens in the SPA

RFC 10017 / BCP 212, *OAuth 2.0 for Browser-Based Applications* (August 2026), describes three architectures in decreasing order of security [1]:

1. **Backend For Frontend (§6.1).** The backend is a confidential client and keeps tokens server-side behind a cookie session. Tokens never reach browser JavaScript. The BCP calls it "strongly recommended for business applications" (§6.1.4.3).
2. **Token-mediating backend (§6.2).** The backend obtains the tokens but hands the access token to the browser. The BCP says to evaluate a full BFF first and to choose this pattern only when proxying is not viable (§6.2.4.4).
3. **Browser-based OAuth client (§6.3).** A public client in the SPA. It is exposed to every attack scenario in §5, including an XSS payload silently acquiring fresh tokens.

ddash fits the BFF case exactly. The SPA is served from the same Go binary (§6.1.2.5 notes that the BFF may serve the static JavaScript itself). ddash's API is its own backend, so there is no third-party resource server for the browser to call. It would not even need to proxy tokens: the Go handlers authorize requests from the session, and in v1 ddash calls no downstream API with the user's access token. (Its integrations use their own API keys.)

Normative BFF requirements from RFC 10017 that ddash must meet:

- "The BFF MUST act as a confidential client" and uses the Authorization Code grant (§6.1.3.1).
- Cookies (§6.1.3.2): `Secure` MUST, `HttpOnly` MUST, `SameSite=Strict` SHOULD, `Path=/` SHOULD, no `Domain` attribute (SHOULD NOT). The cookie name SHOULD carry a prefix showing it was set over HTTP. The RFC names `__Host-Http-`; the widely supported `__Host-` prefix already enforces Secure, no Domain and Path=/. If session state lives in the cookie itself rather than server-side, it SHOULD be encrypted.
- CSRF (§6.1.3.3): "The BFF MUST implement a proper CSRF defense." `SameSite` alone is adequate only if the BFF is never same-site with other apps (§6.1.3.3.1). ddash shares the home domain with other self-hosted apps, so that condition does not hold. The recommended defence is that the BFF "SHOULD require that the browser-based application includes a custom request header" and "MUST ensure that every incoming request carries this static header" (§6.1.3.3.2). Same-origin deployment means legitimate calls need no CORS preflight.

### Go implementation shape (coreos/go-oidc v3 + golang.org/x/oauth2)

- At startup, `oidc.NewProvider(ctx, issuerURL)` performs discovery. `provider.Endpoint()` feeds an `oauth2.Config` whose scopes include `oidc.ScopeOpenID`. `provider.Verifier(&oidc.Config{ClientID: ...})` builds the ID token verifier [4].
- **Login handler**
  - Generate `state`, `nonce` and `oauth2.GenerateVerifier()` (32 random octets) [5].
  - Store all three in a short-lived login cookie, then redirect to `AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))` [4][5].
- **Callback handler**
  - Check `state`, then `Exchange(ctx, code, oauth2.VerifierOption(verifier))` [5].
  - Read the raw ID token with `token.Extra("id_token").(string)` and run `verifier.Verify(ctx, raw)`.
  - **Compare the nonce yourself:** "Verify does NOT do nonce validation, which is the caller's responsibility" [4].
  - Then check that the operator is authorised (see the Keycloak section), create a server-side session (an SQLite row keyed by a random ID), and set the session cookie.
- go-oidc's `RemoteKeySet` caches the JWKS and re-fetches it when it sees an unknown key ID, so Keycloak key rotation needs no work. Reuse a single verifier [4].
- If ddash keeps the refresh token, `Config.TokenSource` refreshes it automatically [5]. ddash v1 does not need to keep any Keycloak token after login. Its session can simply have its own lifetime. Keeping the refresh token is useful only if ddash wants a Keycloak-side logout to end the ddash session early.
- Set `Endpoint.AuthStyle` explicitly (Keycloak accepts HTTP Basic) to avoid the auto-detect probe [5].
- **Logout.** Delete the session, then redirect to Keycloak's `end_session_endpoint` with `id_token_hint` (or `client_id`) and a `post_logout_redirect_uri` that matches the client's configured list [3].

### Cookie detail: Strict vs the OIDC callback

The return from Keycloak to `/auth/callback` is a cross-site top-level navigation whenever Keycloak is on a different site (registrable domain) from ddash, and a `SameSite=Strict` cookie is not sent on it. So:

- The **login-state cookie** (state, nonce and PKCE verifier; about 5–10 minutes) must be `SameSite=Lax`.
- The **session cookie** can be `SameSite=Strict`, as the RFC recommends. It is set on the callback response and sent on the SPA's same-origin requests that follow.

This is inference from standard SameSite semantics, not a quote from the sources. If Keycloak and ddash share a registrable domain (e.g. `auth.home.example` and `ddash.home.example`), the navigation is same-site anyway. That is the condition §6.1.3.3.1 warns about, and it is why the custom-header CSRF check is still required.

## 2. Keycloak client settings

From the Keycloak 26.8 Server Administration Guide, "Creating an OpenID Connect client" → Basic configuration [3]:

| Setting | Value for ddash | Why (docs) |
|---|---|---|
| Client type | OpenID Connect | Keycloak calls OIDC "the recommended protocol to secure applications". |
| **Client authentication** | **On** | "For server-side clients that perform browser logins and require client secrets… This setting should be used for server-side applications." The secret goes in ddash's env-sourced config. |
| Authorization | Off | Fine-grained authorization services are not needed. |
| **Standard flow** | **On** | Enables the Authorization Code flow. |
| Direct access grants | Off | Password grant; not used. |
| Implicit flow | Off | Not used; the BCP rejects it. |
| Service account roles | Off | Client credentials; not used for login. |
| Device grant, CIBA, token exchange | Off | Not used. |
| **PKCE method** | **S256** | With the field blank, Keycloak applies PKCE only if the client sends it. S256 makes it mandatory. |
| Root URL / Home URL | `https://ddash.<domain>` | Used for relative URLs and links back to the client. |
| **Valid redirect URIs** | `https://ddash.<domain>/auth/callback` (exact) | Matching is exact and case-sensitive. A trailing wildcard is allowed, but the full `*` should not be used in production. "Exclusive redirect URI patterns are typically more secure." |
| Valid post logout redirect URIs | `https://ddash.<domain>/` | `post_logout_redirect_uri` must match one of these. |
| Web origins | empty | It only controls CORS for browser JavaScript calling Keycloak, and in a BFF the browser never calls Keycloak's token endpoint. |
| Front-channel logout | Off | Optional. To honour Keycloak-initiated logout, set a **Backchannel logout URL** (`/auth/backchannel-logout`). Keycloak then POSTs an OIDC logout token, and ddash deletes the matching session (by `sid`). This is not needed for v1. |
| Consent required | Off | Single operator, first-party app. |

**Single operator.** Anyone with an account in the realm can complete the login. Gate on a claim in the ID token, such as membership of a `ddash-admin` group or client role. Add a group-membership or role mapper with **Add to ID token** on, then have the callback reject any user without it. This is the standard Keycloak mapper approach; it is not quoted from the admin guide above.

**Issuer stability.** Keycloak's issuer is `https://<keycloak-host>/realms/<realm>` and comes from the `hostname` option. Set `hostname` to the full public URL, so that the issuer is the same whether the browser arrives via LAN, Remote Access or the internet. Fixing the hostname also avoids "tokens [being] issued by a fraudulent issuer" [8]. If ddash reaches Keycloak over the LAN while browsers use the public URL, `hostname-backchannel-dynamic=true` lets the backchannel (token, JWKS) resolve to the internal address. Behind nginx, use `proxy-headers xforwarded` [8]. go-oidc requires the discovered issuer to equal the configured URL, so ddash's `issuerURL` must be the public one [4].

## 3. Exposure through Cloudflare Tunnel with Cloudflare Access

The BFF design is already internet-safe: it does not rely on the network being trusted, as issue #1 requires. Access adds an outer gate. What changes:

1. **Keycloak becomes Access's IdP.** Add a generic OIDC identity provider in Zero Trust using Keycloak's authorization endpoint, token endpoint and `jwks_uri`, and enable PKCE. Register `https://<team>.cloudflareaccess.com/cdn-cgi/access/callback` as a redirect URI on a **separate** Keycloak client for Access [9]. Keycloak's login page and token endpoint must therefore be reachable from the internet (inference: the browser is redirected there, and Cloudflare exchanges the code). Because both logins go through the same Keycloak SSO session, the second login (ddash's own) completes without a prompt.
2. **Validate that requests came through Access.** Access forwards a signed JWT in `Cf-Access-Jwt-Assertion`. Use that header, not the `CF_Authorization` cookie, because "the cookie is not guaranteed to be passed". Check it against `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs` and the application's AUD tag. Cloudflare's own Go example uses `coreos/go-oidc`, the library ddash already depends on [7]. The simplest option is to set `originRequest.access: {required: true, teamName, audTag}` on the tunnel ingress rule, so that `cloudflared` itself validates the JWT before proxying [10]. LAN and Remote Access traffic does not pass through Access, so ddash must not *require* the header globally. Enforce it in `cloudflared`, or only on requests that arrive via the tunnel's internal route.
3. **Cookies.** Access sets its own `CF_Authorization` cookie on the ddash hostname. ddash's `__Host-` session cookie coexists with it and needs no changes. Access's own cookie settings stay at their defaults:
   - HttpOnly stays on.
   - SameSite stays Lax. Cloudflare warns that "Setting the `SameSite` setting to Strict can result in too many redirects".
   - The binding cookie is optional for a browser-only app [6].
4. **CORS and fetch.** Same-origin requests trigger no CORS checks. Cross-origin preflights to an Access app return 403 because browsers send no cookies on OPTIONS [11]. ddash serves its SPA and API from one origin, so this does not apply. The SPA should use `fetch(..., {credentials: 'same-origin'})` (the default) [11].
   - **Expired Access session.** When it expires, Access redirects API calls to the login page, which looks to `fetch` like a CORS or network error. The SPA should treat any non-JSON or opaque failure on `/api` as "session expired" and do a full-page reload, which lets Access and then ddash re-authenticate. This is inference from [11], which says a missing cookie causes a CORS error and the fix is to log in and refresh.
   - Similarly, ddash's own `401` from `/api` should trigger `window.location = '/auth/login'`, never an XHR to the login endpoint.
5. **Session lengths.** The Access application session defaults to 24 h [6]. Pick a ddash session lifetime that is no longer than the Access one, or accept the occasional reload-driven re-login.
6. **Scheme and host.** Behind `cloudflared`, ddash should build its redirect URI from config (its public base URL), not from the request's `Host` header or scheme. The same applies to the nginx reverse proxy on the LAN.

## Recommendation

Build ddash's auth as a **BFF**:

- A confidential Keycloak client with Standard flow only, PKCE S256 and an exact redirect URI.
- `coreos/go-oidc/v3` with `golang.org/x/oauth2` for code + PKCE + nonce. Check the nonce manually.
- A server-side session in SQLite, referenced by a `__Host-` cookie that is `Secure; HttpOnly; SameSite=Strict; Path=/`. A separate `SameSite=Lax` cookie carries the login state.
- A required custom header (e.g. `X-Requested-By: ddash`) on all non-GET `/api` calls as the CSRF defence.
- An ID-token group or role claim restricting login to the operator.
- Keycloak's `hostname` fixed to its public URL, with ddash configured with that issuer and with its own public base URL.

For later internet exposure, put Cloudflare Access (Keycloak as a generic OIDC IdP) in front of the tunnel hostname, and turn on `cloudflared`'s `access.required` with the app's AUD tag. No change to ddash's own auth code is needed, other than the SPA treating a failed `/api` fetch as a cue to reload.

## Sources

- [1] RFC 10017 / BCP 212, *OAuth 2.0 for Browser-Based Applications*, IETF, Aug 2026: https://www.rfc-editor.org/rfc/rfc10017.html (formerly draft-ietf-oauth-browser-based-apps: https://datatracker.ietf.org/doc/draft-ietf-oauth-browser-based-apps/). §6, §6.1.2.5, §6.1.3.1–6.1.3.3, §6.1.4.3, §6.2.4.4, §6.3.
- [3] Keycloak 26.8 Server Administration Guide, "Creating an OpenID Connect client", "Basic configuration" and the OIDC logout section: https://www.keycloak.org/docs/latest/server_admin/index.html
- [4] `github.com/coreos/go-oidc/v3/oidc` package docs (v3.21.0): https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc
- [5] `golang.org/x/oauth2` package docs (v0.37.0): https://pkg.go.dev/golang.org/x/oauth2
- [6] Cloudflare One, "Authorization cookie": https://developers.cloudflare.com/cloudflare-one/identity/authorization-cookie/
- [7] Cloudflare One, "Validate JWTs": https://developers.cloudflare.com/cloudflare-one/identity/authorization-cookie/validating-json/
- [8] Keycloak, "Configuring the hostname (v2)": https://www.keycloak.org/server/hostname
- [9] Cloudflare One, "Generic OIDC" identity provider: https://developers.cloudflare.com/cloudflare-one/integrations/identity-providers/generic-oidc/
- [10] Cloudflare Tunnel, "Origin parameters" (`access`): https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/cloudflared-parameters/origin-parameters/
- [11] Cloudflare One, "CORS" (Access): https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/cors/
