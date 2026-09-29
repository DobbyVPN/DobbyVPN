"""Trusted hosted-test infrastructure controllers.

Provider code owns infrastructure lifecycle only.  Functional scenario
meaning and assertions remain in :mod:`torturer_contract.functional`.
"""

from .render import (
    HTTPResponse,
    RenderAPI,
    RenderAPIError,
    RenderServiceHandle,
    RenderServiceSpec,
    RenderServiceReady,
    DisposableRenderController,
)

__all__ = [
    "DisposableRenderController",
    "HTTPResponse",
    "RenderAPI",
    "RenderAPIError",
    "RenderServiceHandle",
    "RenderServiceReady",
    "RenderServiceSpec",
]
