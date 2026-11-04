"""Verifies the JWT issued by the Go API (HS256, shared secret).

The tenant used for every knowledge-base query comes from the token, never
from the request body.
"""

from dataclasses import dataclass

import jwt
from fastapi import Depends, HTTPException, Request


@dataclass(frozen=True)
class Identity:
    user_id: int
    tenant_id: int
    role: str


def decode_token(token: str, secret: str) -> Identity:
    try:
        claims = jwt.decode(token, secret, algorithms=["HS256"])
        return Identity(
            user_id=int(claims["user_id"]),
            tenant_id=int(claims["tenant_id"]),
            role=str(claims["role"]),
        )
    except (jwt.PyJWTError, KeyError, ValueError, TypeError):
        raise HTTPException(status_code=401, detail="invalid token") from None


def current_identity(request: Request) -> Identity:
    header = request.headers.get("authorization", "")
    scheme, _, token = header.partition(" ")
    if scheme.lower() != "bearer" or not token:
        raise HTTPException(status_code=401, detail="missing bearer token")
    return decode_token(token, request.app.state.settings.jwt_secret)


CurrentIdentity = Depends(current_identity)
