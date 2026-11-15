"""Verifies the JWT issued by the Go API (HS256, shared secret).

The tenant used for every knowledge-base query comes from the token, never
from the request body.
"""

from dataclasses import dataclass

import jwt
from fastapi import Depends, HTTPException, Request


# Must match the Go API (internal/auth): tokens minted elsewhere with the same
# secret are rejected.
TOKEN_ISSUER = "omnidesk"
TOKEN_AUDIENCE = "omnidesk-api"
ROLES = frozenset({"admin", "agent"})


@dataclass(frozen=True)
class Identity:
    user_id: int
    tenant_id: int
    role: str


def decode_token(token: str, secret: str) -> Identity:
    try:
        claims = jwt.decode(
            token,
            secret,
            algorithms=["HS256"],
            issuer=TOKEN_ISSUER,
            audience=TOKEN_AUDIENCE,
            options={"require": ["exp", "iss", "aud"]},
        )
        who = Identity(
            user_id=int(claims["user_id"]),
            tenant_id=int(claims["tenant_id"]),
            role=str(claims["role"]),
        )
    except (jwt.PyJWTError, KeyError, ValueError, TypeError):
        raise HTTPException(status_code=401, detail="invalid token") from None
    if who.user_id < 1 or who.tenant_id < 1 or who.role not in ROLES:
        raise HTTPException(status_code=401, detail="invalid token")
    return who


def current_identity(request: Request) -> Identity:
    header = request.headers.get("authorization", "")
    scheme, _, token = header.partition(" ")
    if scheme.lower() != "bearer" or not token:
        raise HTTPException(status_code=401, detail="missing bearer token")
    return decode_token(token, request.app.state.settings.jwt_secret)


def admin_identity(who: Identity = Depends(current_identity)) -> Identity:
    """Knowledge-base writes are admin-only, matching the Go API's roles."""
    if who.role != "admin":
        raise HTTPException(status_code=403, detail="this action requires role: admin")
    return who


CurrentIdentity = Depends(current_identity)
AdminIdentity = Depends(admin_identity)
