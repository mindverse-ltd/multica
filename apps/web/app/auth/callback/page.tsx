"use client";

import { Suspense, useCallback, useEffect, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { sanitizeNextUrl, useAuthStore } from "@multica/core/auth";
import type { FeishuNeedsEmailResponse, LoginResponse } from "@multica/core/api";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { paths, resolvePostAuthDestination } from "@multica/core/paths";
import { api } from "@multica/core/api";
import { validateCliCallback, redirectToCliCallback } from "@multica/views/auth";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Loader2 } from "lucide-react";

function isFeishuNeedsEmail(resp: unknown): resp is FeishuNeedsEmailResponse {
  return (
    typeof resp === "object" &&
    resp !== null &&
    "needs_email" in resp &&
    (resp as FeishuNeedsEmailResponse).needs_email === true
  );
}

function CallbackContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const qc = useQueryClient();
  const loginWithGoogle = useAuthStore((s) => s.loginWithGoogle);
  const setUser = useAuthStore((s) => s.setUser);
  const [error, setError] = useState("");
  const [desktopToken, setDesktopToken] = useState<string | null>(null);

  const postLogin = useCallback(
    async (loggedInUser: { id: string; onboarded_at: string | null }) => {
      const wsList = await api.listWorkspaces();
      qc.setQueryData(workspaceKeys.list(), wsList);
      const onboarded = loggedInUser.onboarded_at != null;

      const nextPart = searchParams
        .get("state")
        ?.split(",")
        .find((p) => p.startsWith("next:"));
      // Strip "next:" prefix, then drop anything that isn't a safe relative path
      // so an attacker-controlled `state=next:https://evil` cannot redirect here.
      const nextUrl = sanitizeNextUrl(nextPart ? nextPart.slice(5) : null);

      // 1. nextUrl wins: a `next=/invite/<id>` always survives the OAuth
      //    round-trip — the user clicked a specific link and we should
      //    honor exactly that destination.
      if (nextUrl) {
        router.push(nextUrl);
        return;
      }

      // 2. Un-onboarded users may have pending invitations on their
      //    email even when no `next=` was carried (came from a fresh
      //    login on app.multica.ai instead of clicking the email link,
      //    or `state` was lost across the round-trip). Look them up by
      //    email and route to the batch /invitations page if any.
      //    Already-onboarded users skip this lookup — their new invites
      //    surface in the sidebar dropdown, not as a forced wall.
      if (!onboarded) {
        try {
          const invites = await api.listMyInvitations();
          if (invites.length > 0) {
            qc.setQueryData(workspaceKeys.myInvitations(), invites);
            router.push(paths.invitations());
            return;
          }
        } catch {
          // Network blip on the invite lookup is non-fatal — fall through
          // to the normal post-auth destination so the user isn't stuck
          // on a blank callback screen. Worst case they land on
          // /onboarding and the sidebar will surface invites later.
        }
      }

      // 3. Default: hand off to the resolver (onboarding for first-timers,
      //    first workspace for returning users, /workspaces/new for
      //    onboarded users with zero workspaces).
      router.push(resolvePostAuthDestination(wsList, onboarded));
    },
    [router, qc, searchParams],
  );

  const handleFeishuLogin = useCallback(
    async (code: string, redirectUri: string) => {
      const response = await api.feishuLogin(code, redirectUri);

      if (isFeishuNeedsEmail(response)) {
        const params = new URLSearchParams({
          bind_email: response.session_token,
        });
        if (response.name) params.set("name", response.name);
        if (response.avatar_url) params.set("avatar_url", response.avatar_url);
        router.push(`${paths.login()}?${params.toString()}`);
        return;
      }

      const { token, user } = response as LoginResponse;
      api.setToken(token);
      setUser(user);
      await postLogin(user);
    },
    [router, setUser, postLogin],
  );

  useEffect(() => {
    const code = searchParams.get("code");
    if (!code) {
      setError("Missing authorization code");
      return;
    }

    const errorParam = searchParams.get("error");
    if (errorParam) {
      setError(errorParam === "access_denied" ? "Access denied" : errorParam);
      return;
    }

    const state = searchParams.get("state") || "";
    const stateParts = state.split(",").filter(Boolean);
    const providerPart = stateParts.find((p) => p.startsWith("provider:"));
    const provider =
      providerPart?.slice(9) === "feishu" ? "feishu" : "google";
    const isDesktop = stateParts.includes("platform:desktop");

    // CLI callback params — carried across the Google OAuth round-trip so
    // headless/WSL2 `multica login` can receive the JWT after browser-based
    // Google auth completes.
    const cliCallbackPart = stateParts.find((p) => p.startsWith("cli_callback:"));
    const cliStatePart = stateParts.find((p) => p.startsWith("cli_state:"));
    const cliCallbackRaw = cliCallbackPart
      ? decodeURIComponent(cliCallbackPart.slice("cli_callback:".length))
      : null;
    const cliState = cliStatePart
      ? decodeURIComponent(cliStatePart.slice("cli_state:".length))
      : "";

    const redirectUri = `${window.location.origin}/auth/callback`;

    // Validate the CLI callback URL before redirecting — the state parameter
    // passes through OAuth and must be treated as attacker-controlled.
    const cliCallback =
      cliCallbackRaw && validateCliCallback(cliCallbackRaw)
        ? cliCallbackRaw
        : null;

    if (cliCallback) {
      if (provider === "feishu") {
        setError("CLI callback is not supported for Feishu login");
        return;
      }
      api
        .googleLogin(code, redirectUri)
        .then(({ token }) => {
          redirectToCliCallback(cliCallback, token, cliState);
        })
        .catch((err) => {
          setError(err instanceof Error ? err.message : "Login failed");
        });
      return;
    }

    if (isDesktop) {
      const exchangeToken =
        provider === "feishu"
          ? api.feishuLogin.bind(api)
          : api.googleLogin.bind(api);
      exchangeToken(code, redirectUri)
        .then((result) => {
          if (isFeishuNeedsEmail(result)) {
            setError("Email binding is not supported in desktop mode");
            return;
          }
          setDesktopToken(result.token);
          window.location.href = `multica://auth/callback?token=${encodeURIComponent(result.token)}`;
        })
        .catch((err) => {
          setError(err instanceof Error ? err.message : "Login failed");
        });
      return;
    }

    if (provider === "feishu") {
      handleFeishuLogin(code, redirectUri).catch((err) => {
        setError(err instanceof Error ? err.message : "Login failed");
      });
      return;
    }

    loginWithGoogle(code, redirectUri)
      .then((loggedInUser) => postLogin(loggedInUser))
      .catch((err) => {
        setError(err instanceof Error ? err.message : "Login failed");
      });
  }, [searchParams, loginWithGoogle, handleFeishuLogin, postLogin]);

  if (desktopToken) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-display-sm">Opening Multica</CardTitle>
            <CardDescription>
              You should see a prompt to open the Multica desktop app. If
              nothing happens, click the button below.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            <Button
              variant="outline"
              onClick={() => {
                window.location.href = `multica://auth/callback?token=${encodeURIComponent(desktopToken)}`;
              }}
            >
              Open Multica Desktop
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-display-sm">Login Failed</CardTitle>
            <CardDescription>{error}</CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            <a
              href={paths.login()}
              className="text-primary underline-offset-4 hover:underline"
            >
              Back to login
            </a>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">Signing in...</CardTitle>
          <CardDescription>
            Please wait while we complete your login
          </CardDescription>
        </CardHeader>
        <CardContent className="flex justify-center">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </CardContent>
      </Card>
    </div>
  );
}

export default function CallbackPage() {
  return (
    <Suspense fallback={null}>
      <CallbackContent />
    </Suspense>
  );
}
