"""Minimal SigV4-signed S3 client for Garage, standard library only.

Garage rejects any request signed for a region other than its configured
``s3_region`` with a 400 — the failure mode behind the 2026-08-09 WAL incident —
so the region is threaded through explicitly rather than left to a default.

Only the four operations the gateway needs are implemented: PUT, GET, HEAD and
ListObjectsV2.
"""

import datetime
import hashlib
import hmac
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

_ALGORITHM = "AWS4-HMAC-SHA256"
_SERVICE = "s3"
_EMPTY_SHA256 = hashlib.sha256(b"").hexdigest()
_S3_NS = "{http://s3.amazonaws.com/doc/2006-03-01/}"


class S3Error(Exception):
    def __init__(self, status, body=b""):
        self.status = status
        self.body = body
        super().__init__(f"S3 returned {status}: {body[:400]!r}")


def _sign(key: bytes, msg: str) -> bytes:
    return hmac.new(key, msg.encode("utf-8"), hashlib.sha256).digest()


def _quote(value: str, safe: str = "") -> str:
    # Python's quote() already leaves the RFC 3986 unreserved set alone.
    return urllib.parse.quote(value, safe=safe)


class S3Client:
    def __init__(self, endpoint, region, bucket, access_key, secret_key, timeout=300):
        self.endpoint = endpoint.rstrip("/")
        self.region = region
        self.bucket = bucket
        self.access_key = access_key
        self.secret_key = secret_key
        self.timeout = timeout
        self._host = urllib.parse.urlsplit(self.endpoint).netloc

    # -- signing ---------------------------------------------------------

    def _signing_key(self, datestamp):
        k = _sign(("AWS4" + self.secret_key).encode("utf-8"), datestamp)
        k = _sign(k, self.region)
        k = _sign(k, _SERVICE)
        return _sign(k, "aws4_request")

    def _auth_headers(self, method, canonical_uri, query, payload_sha, extra):
        now = datetime.datetime.now(datetime.timezone.utc)
        amzdate = now.strftime("%Y%m%dT%H%M%SZ")
        datestamp = now.strftime("%Y%m%d")

        headers = {
            "host": self._host,
            "x-amz-content-sha256": payload_sha,
            "x-amz-date": amzdate,
        }
        for name, value in (extra or {}).items():
            headers[name.lower()] = value

        signed_names = sorted(headers)
        canonical_headers = "".join(f"{n}:{headers[n].strip()}\n" for n in signed_names)
        signed_headers = ";".join(signed_names)

        canonical_query = "&".join(
            f"{_quote(k)}={_quote(str(v))}" for k, v in sorted((query or {}).items())
        )
        canonical_request = "\n".join(
            [method, canonical_uri, canonical_query, canonical_headers,
             signed_headers, payload_sha]
        )

        scope = f"{datestamp}/{self.region}/{_SERVICE}/aws4_request"
        string_to_sign = "\n".join([
            _ALGORITHM,
            amzdate,
            scope,
            hashlib.sha256(canonical_request.encode("utf-8")).hexdigest(),
        ])
        signature = hmac.new(
            self._signing_key(datestamp), string_to_sign.encode("utf-8"), hashlib.sha256
        ).hexdigest()

        headers["Authorization"] = (
            f"{_ALGORITHM} Credential={self.access_key}/{scope}, "
            f"SignedHeaders={signed_headers}, Signature={signature}"
        )
        return headers

    # -- transport -------------------------------------------------------

    def _request(self, method, key="", query=None, body=None, body_length=None,
                 payload_sha=None, extra_headers=None):
        canonical_uri = "/" + self.bucket + "/" + _quote(key, safe="/")
        url = self.endpoint + canonical_uri
        canonical_query = "&".join(
            f"{_quote(k)}={_quote(str(v))}" for k, v in sorted((query or {}).items())
        )
        if canonical_query:
            url += "?" + canonical_query

        extra = dict(extra_headers or {})
        if body is not None and body_length is not None:
            extra["content-length"] = str(body_length)

        headers = self._auth_headers(
            method, canonical_uri, query, payload_sha or _EMPTY_SHA256, extra
        )
        request = urllib.request.Request(url, data=body, method=method)
        for name, value in headers.items():
            request.add_header(name, value)
        try:
            return urllib.request.urlopen(request, timeout=self.timeout)
        except urllib.error.HTTPError as exc:
            raise S3Error(exc.code, exc.read()) from exc

    # -- operations ------------------------------------------------------

    def put(self, key, body, length, sha256_hex, content_type=None):
        extra = {}
        if content_type:
            extra["content-type"] = content_type
        with self._request("PUT", key, body=body, body_length=length,
                           payload_sha=sha256_hex, extra_headers=extra) as response:
            response.read()

    def get(self, key):
        """Return the raw response; the caller is responsible for closing it."""
        return self._request("GET", key)

    def exists(self, key):
        try:
            with self._request("HEAD", key) as response:
                response.read()
            return True
        except S3Error as exc:
            if exc.status == 404:
                return False
            raise

    def list_keys(self, prefix, start_after=None, limit=1000):
        """Return up to ``limit`` keys under ``prefix``, lexicographically sorted."""
        keys = []
        token = None
        while len(keys) < limit:
            query = {
                "list-type": "2",
                "prefix": prefix,
                "max-keys": str(min(1000, limit - len(keys))),
            }
            if start_after:
                query["start-after"] = start_after
            if token:
                query["continuation-token"] = token
            with self._request("GET", "", query=query) as response:
                root = ET.fromstring(response.read())
            for contents in root.findall(f"{_S3_NS}Contents"):
                node = contents.find(f"{_S3_NS}Key")
                if node is not None and node.text:
                    keys.append(node.text)
            truncated = root.find(f"{_S3_NS}IsTruncated")
            if truncated is None or truncated.text != "true":
                break
            token_node = root.find(f"{_S3_NS}NextContinuationToken")
            if token_node is None or not token_node.text:
                break
            token = token_node.text
        return keys
