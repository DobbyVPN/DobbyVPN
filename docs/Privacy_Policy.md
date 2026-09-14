# Privacy policy

DobbyVPN does not send app telemetry to DobbyVPN servers. The app does contact
third-party services as part of normal operation:

- Before a new tunnel is established, DobbyVPN downloads a URL-based
  subscription directly over HTTPS. Redirects must remain HTTPS, and the
  response is limited to 1 MiB. The subscription server can observe the request
  and the network address from which it arrives.
- Connection selection and health checks request Google, Cloudflare, and
  `about.google` endpoints over the active tunnel route. Those providers handle
  the requests and may observe the network metadata available to them.
- When connected, the endpoint in the user's profile handles traffic routed
  through that tunnel and may observe information exposed by the selected
  protocol. DobbyVPN does not operate or control that endpoint.
- VPN DNS settings vary by platform. Android advertises Cloudflare resolvers
  (`1.1.1.1` and `2606:4700:4700::1111`) to its VPN service; iOS advertises
  `1.1.1.1` and `8.8.8.8`. The effective resolver path can also depend on the
  selected protocol and its runtime settings. Android's advertised resolver
  list is fixed and is independent of TrustTunnel's `dns_upstreams` setting.
- If the user runs the CLI `external-ip` command, it requests their public
  address from `api.ipify.org`, then tries `ifconfig.me/ip` if the first
  service is unavailable. Those services can observe the request and the
  network address from which it arrives.
- Diagnostic logs are stored locally. On Android and iOS, exporting logs
  creates a compressed file and opens the platform share sheet; on desktop, it
  opens a save dialog. DobbyVPN does not transmit logs automatically. If the
  user chooses another app or service as the destination, that recipient gets
  the exported file.

`ExcludeIPs` deliberately routes the listed destinations outside the VPN or
proxy path. This behavior can expose those connections to the network they use.
Remote telemetry was removed; legacy `[Telemetry]` configuration is rejected.
