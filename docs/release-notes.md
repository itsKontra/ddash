# Release notes

What an Operator does on updating, per Release, newest first. Cutting a
Release moves **Unreleased** under its tag ([releasing](releasing.md)).

## Unreleased

- An Action you confirmed now runs to its end even when you close the tab,
  and ddash lets a running Action finish before it stops for an update. Set
  `stop_grace_period: 90s` on the ddash service in your compose file, as
  [`deploy/compose.yaml`](../deploy/compose.yaml) does, so Docker gives it
  that time.

## v0.0.2-rc.3

- ddash no longer reads nginx over SSH: the Reverse Proxy and Apps read
  Nginx Proxy Manager's API. Remove ddash's line (`command="/usr/sbin/nginx -T"`)
  from root's `authorized_keys` on the nginx host. Its old SSH settings are
  dropped on the update, so the Reverse Proxy and Apps ask you to set up
  Nginx Proxy Manager: create ddash's user there
  ([deployment](deployment.md#6-the-systems-ddash-reaches)) and fill in
  nginx-proxy-manager-api's config in Plugins.
- Apps shows each App's network info, so it now requires network, and with
  it adguard-api. Without the Network Plugin installed, fill in their
  config in Plugins; until then Apps work without the network info.
