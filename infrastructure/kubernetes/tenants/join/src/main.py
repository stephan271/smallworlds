"""join — Member Invitations (docs/adr/0058). A stdlib HTTP server, two listeners.

- PUBLIC_PORT: the join page and its three API calls. `/desk` does not exist here.
- DESK_PORT: the Invitation Desk (desk.py). The public Ingress never routes to
  it, so if the private route ever breaks, /desk fails closed (design §9).

The code never appears in a URL path: the page reads it from the fragment and
POSTs it, so it never reaches Traefik's access log, Loki or a Referer.
"""
import logging
import threading
from http.server import ThreadingHTTPServer

import config
import desk
import member
import store
import web

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("main")

_PAGES = {"/": "index.html", "/done/de": "done.html", "/done/en": "done.html"}

_ROUTES = {
    "/api/invitation": lambda body, source: member.lookup(body.get("code"), source),
    "/api/username": lambda body, source: member.check_username(
        body.get("code"), body.get("username"), source),
    "/api/redeem": lambda body, source: member.redeem(body.get("code"), body, source),
}


class PublicHandler(web.BaseHandler):
    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self.send(200, b"OK", "text/plain")
        elif path in _PAGES:
            self.send_file(_PAGES[path], "no-store")
        elif path.startswith("/static/"):
            self.send_file(path[len("/static/"):], "public, max-age=300")
        elif path == "/api/config":
            self.send_json(200, {"dashboardUrl": config.DASHBOARD_URL})
        else:
            self.send_json(404, {"error": "not_found"})

    do_HEAD = do_GET

    def do_POST(self):
        path = self.path.split("?", 1)[0]
        if path not in _ROUTES:
            self.send_json(404, {"error": "not_found"})
            return
        body, refusal = self.read_json()
        if refusal:
            self.send_json(*refusal)
            return
        try:
            status, payload = _ROUTES[path](body, self.source())
        except Exception:
            log.exception("%s failed", path)
            status, payload = 500, {"error": "unavailable"}
        self.send_json(status, payload)


def serve_public(port: int = config.PUBLIC_PORT) -> ThreadingHTTPServer:
    return ThreadingHTTPServer((config.BIND, port), PublicHandler)


def serve_desk(port: int = config.DESK_PORT) -> ThreadingHTTPServer:
    return ThreadingHTTPServer((config.BIND, port), desk.DeskHandler)


def main():
    if not config.CLIENT_SECRET:
        log.warning("KEYCLOAK_CLIENT_SECRET is empty — every invitation will fail at Keycloak")
    store.init()
    desk_server = serve_desk()
    threading.Thread(target=desk_server.serve_forever, daemon=True, name="desk").start()
    log.info("Invitation Desk listening on %s:%s", config.BIND, desk_server.server_port)
    server = serve_public()
    log.info("join page listening on %s:%s", config.BIND, server.server_port)
    server.serve_forever()


if __name__ == "__main__":
    main()
