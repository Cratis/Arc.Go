# Minimal HTTP hosting

Run from the repository root with Go 1.26 or later:

```sh
go run ./examples/hosting
```

In another terminal:

```sh
curl -H 'Content-Type: application/json' -d '{"name":"Ada"}' http://127.0.0.1:8080/api/greet
```

The successful command envelope includes `"response":"Hello, Ada"`. Posting the
same body to `/api/greet/validate` succeeds without invoking `Handle`. Empty names
return 400. Press Ctrl+C to drain and stop the owned server.

This example binds loopback, uses no container or persistence, and does not enable
anonymous production discovery or credential verification. Configure trusted
authentication and operation authorization before exposing a private service.
