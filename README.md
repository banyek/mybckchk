# MySQL backend checker

This tool is able to run user defined SQL queries against a MySQL instance and based on their result, reports HTTP/200 or HTTP/500 on a predefined port. You can use it as a backend check for HAProxy implementing MySQL HA.

## Configuration

Copy `mybckchk.cfg` and set the credentials for a dedicated MySQL user.
Grant it only the privileges needed by your configured queries. Set
`mysql_db` if your queries need a default schema. Keep the
configuration file readable only by the service account and its administrators.

Each check section requires a query and an expected first-column value.
A failed query, missing row, or unexpected value marks the backend unavailable.
The first check starts immediately. The endpoint returns HTTP 500 until a
complete cycle succeeds. A success expires after one check interval plus one
check timeout. Both settings are in milliseconds.

- `listen` defaults to port 9200. Set `listen_host` to bind to a specific
  address. An omitted host listens on all interfaces.
- `mysql_host` defaults to localhost over TCP. Set `mysql_socket` and omit
  `mysql_host` to connect through a Unix socket.
- Remote TCP connections use certificate-verified TLS by default, using the
  system trust store. For loopback connections, set `mysql_tls = true` to
  require TLS. Set `mysql_tls = false` only when plaintext is intentional.
- The `-enable` and `-disable` flags override database checks and continue
  serving the corresponding HTTP status.
