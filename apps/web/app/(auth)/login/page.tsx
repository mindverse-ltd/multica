"use client";

import { Suspense, useEffect, useState, type FormEvent } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { sanitizeNextUrl, useAuthStore } from "@multica/core/auth";
import { workspaceKeys } from "@multica/core/workspace/queries";
import {
  paths,
  resolvePostAuthDestination,
  useHasOnboarded,
} from "@multica/core/paths";
import { api } from "@multica/core/api";
import type { Workspace } from "@multica/core/types";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Loader2 } from "lucide-react";
import { setLoggedInCookie } from "@/features/auth/auth-cookie";
import { LoginPage, validateCliCallback } from "@multica/views/auth";
import { useConfigStore } from "@multica/core/config";
import { useT } from "@multica/views/i18n";

/**
 * Pick where a logged-in user with no explicit `?next=` should land.
 * Un-onboarded users with pending invitations on their email get routed to
 * the batch /invitations page; everyone else falls through to the standard
 * resolver. A network blip on listMyInvitations is non-fatal — we fall
 * through rather than trap the user on an error screen.
 */
async function resolveLoggedInDestination(
  qc: QueryClient,
  hasOnboarded: boolean,
  workspaces: Workspace[],
): Promise<string> {
  if (!hasOnboarded) {
    try {
      const invites = await api.listMyInvitations();
      if (invites.length > 0) {
        qc.setQueryData(workspaceKeys.myInvitations(), invites);
        return paths.invitations();
      }
    } catch {
      // fall through
    }
  }
  return resolvePostAuthDestination(workspaces, hasOnboarded);
}

function LoginPageContent() {
  const router = useRouter();
  const qc = useQueryClient();
  const { t } = useT("auth");
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const searchParams = useSearchParams();

  const cliCallbackRaw = searchParams.get("cli_callback");
  const cliState = searchParams.get("cli_state") || "";
  const platform = searchParams.get("platform");
  const provider = searchParams.get("provider");
  const isDesktopHandoff = platform === "desktop" && !cliCallbackRaw;
  // `next` carries a protected URL the user was originally headed to
  // (e.g. /invite/{id}). With URL-driven workspaces there is no legacy
  // "/issues" default — if `next` is absent we decide after login based on
  // the user's workspace list. Sanitize first so a crafted `?next=https://evil`
  // cannot bounce the user off-origin after a successful login.
  const nextUrl = sanitizeNextUrl(searchParams.get("next"));
  const bindEmailSessionToken = searchParams.get("bind_email");
  const googleClientId = useConfigStore((s) => s.googleClientId);
  const feishuClientId = useConfigStore((s) => s.feishuAppId);

  const [desktopToken, setDesktopToken] = useState<string | null>(null);
  const [desktopError, setDesktopError] = useState("");
  const [passwordEmail, setPasswordEmail] = useState("");
  const [passwordValue, setPasswordValue] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [passwordLoading, setPasswordLoading] = useState(false);
  const hasOnboarded = useHasOnboarded();

  // Latched once auth has been observed settled as logged-out on this page.
  // Any `user` that appears afterwards came from the login form in this
  // session — not from an existing session found on arrival.
  const settledLoggedOutRef = useRef(false);

  // Already authenticated ON ARRIVAL — honor ?next= or fall back to first
  // workspace (or /onboarding if the user has none). Skip this entire path
  // when the user arrived to authorize the CLI.
  useEffect(() => {
    if (isLoading) return;
    if (!user) {
      settledLoggedOutRef.current = true;
      return;
    }
    if (cliCallbackRaw) return;
    if (isDesktopHandoff) {
      // Desktop opened the browser for login but the web session is already
      // authenticated — mint a bearer token from the cookie session and hand
      // it off via deep link instead of silently redirecting to the workspace.
      api
        .issueCliToken()
        .then(({ token }) => {
          setDesktopToken(token);
          window.location.href = `multica://auth/callback?token=${encodeURIComponent(token)}`;
        })
        .catch((err) => {
          setDesktopError(
            err instanceof Error
              ? err.message
              : t(($) => $.web.desktop_handoff.prepare_failed),
          );
        });
      return;
    }
    // Fresh form login (issue #5009): `user` was written by verifyCode while
    // handleVerify was still fetching the workspace list, so this effect used
    // to read the not-yet-seeded list cache and race handleSuccess with a
    // replace to /workspaces/new. handleSuccess owns post-login navigation;
    // this effect only serves visitors who arrived already authenticated.
    if (settledLoggedOutRef.current) return;
    if (nextUrl) {
      router.replace(nextUrl);
      return;
    }
    // Fetch instead of reading the cache: on a fresh page load the cache is
    // cold, and `getQueryData() ?? []` would misroute a user who does have
    // workspaces to /workspaces/new. On fetch failure fall back to [] —
    // same destination the cold-cache read produced, rather than trapping
    // the user on the login page.
    void qc
      .ensureQueryData(workspaceListOptions())
      .catch(() => [] as Workspace[])
      .then((list) => resolveLoggedInDestination(qc, hasOnboarded, list))
      .then((dest) => router.replace(dest));
  }, [isLoading, user, router, nextUrl, cliCallbackRaw, isDesktopHandoff, hasOnboarded, qc]);

  const handleSuccess = async () => {
    // Read the latest user snapshot directly — the closure's `hasOnboarded`
    // was captured before login completed and would be stale here.
    const currentUser = useAuthStore.getState().user;
    const onboarded = currentUser?.onboarded_at != null;
    if (nextUrl) {
      router.push(nextUrl);
      return;
    }
    const list = qc.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
    router.push(await resolveLoggedInDestination(qc, onboarded, list));
  };

  const handlePasswordLogin = async (e: FormEvent) => {
    e.preventDefault();
    setPasswordLoading(true);
    setPasswordError("");
    try {
      const { token, user } = await api.passwordLogin(passwordEmail, passwordValue);
      api.setToken(token);
      useAuthStore.getState().setUser(user);
      qc.setQueryData(workspaceKeys.list(), await api.listWorkspaces());
      setLoggedInCookie();
      await handleSuccess();
    } catch (err) {
      setPasswordError(
        err instanceof Error ? err.message : "Invalid email or password",
      );
    } finally {
      setPasswordLoading(false);
    }
  };

  // OAuth state carries provider, platform, destination, and CLI callback
  // params across the provider redirect so the callback can finish the
  // correct web, desktop, or CLI login flow.
  const buildProviderState = (providerName: "google" | "feishu") =>
    [
      `provider:${providerName}`,
      platform === "desktop" ? "platform:desktop" : "",
      nextUrl ? `next:${nextUrl}` : "",
      providerName === "google" && cliCallbackRaw && validateCliCallback(cliCallbackRaw)
        ? `cli_callback:${encodeURIComponent(cliCallbackRaw)}`
        : "",
      providerName === "google" && cliState
        ? `cli_state:${encodeURIComponent(cliState)}`
        : "",
    ]
      .filter(Boolean)
      .join(",") || undefined;

  const passwordLoginForm = (
    <div className="w-full space-y-4 pt-1 text-left">
      <div className="relative">
        <div className="absolute inset-0 flex items-center">
          <span className="w-full border-t" />
        </div>
        <div className="relative flex justify-center text-xs uppercase">
          <span className="bg-card px-2 text-muted-foreground">
            Temporary account
          </span>
        </div>
      </div>
      <form
        id="password-login-form"
        onSubmit={handlePasswordLogin}
        className="space-y-4"
      >
        <div className="space-y-2">
          <Label htmlFor="password-login-email">Email</Label>
          <Input
            id="password-login-email"
            type="email"
            value={passwordEmail}
            onChange={(e) => setPasswordEmail(e.target.value)}
            autoComplete="username"
            required
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="password-login-password">Password</Label>
          <Input
            id="password-login-password"
            type="password"
            value={passwordValue}
            onChange={(e) => setPasswordValue(e.target.value)}
            autoComplete="current-password"
            required
          />
        </div>
        {passwordError && (
          <p className="text-sm text-destructive">{passwordError}</p>
        )}
        <Button
          type="submit"
          className="w-full"
          size="lg"
          disabled={!passwordEmail || !passwordValue || passwordLoading}
        >
          {passwordLoading ? "Signing in..." : "Sign in with password"}
        </Button>
      </form>
    </div>
  );

  // While the desktop handoff is in progress (or has produced a token/error),
  // render a dedicated screen instead of flashing the login form or redirecting
  // away to a workspace page.
  if (isDesktopHandoff && user) {
    if (desktopError) {
      return (
        <div className="flex min-h-screen items-center justify-center">
          <Card className="w-full max-w-sm">
            <CardHeader className="text-center">
              <CardTitle className="text-2xl">
                {t(($) => $.web.desktop_handoff.failed_title)}
              </CardTitle>
              <CardDescription>{desktopError}</CardDescription>
            </CardHeader>
          </Card>
        </div>
      );
    }
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <CardTitle className="text-2xl">
              {t(($) => $.web.desktop_handoff.opening_title)}
            </CardTitle>
            <CardDescription>
              {desktopToken
                ? t(($) => $.web.desktop_handoff.opening_description)
                : t(($) => $.web.desktop_handoff.preparing)}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            {desktopToken ? (
              <Button
                variant="outline"
                onClick={() => {
                  window.location.href = `multica://auth/callback?token=${encodeURIComponent(desktopToken)}`;
                }}
              >
                {t(($) => $.web.desktop_handoff.open_button)}
              </Button>
            ) : (
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
            )}
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <LoginPage
      onSuccess={handleSuccess}
      google={
        googleClientId
          ? {
              clientId: googleClientId,
              redirectUri: `${window.location.origin}/auth/callback`,
              state: buildProviderState("google"),
            }
          : undefined
      }
      feishu={
        feishuClientId
          ? {
              clientId: feishuClientId,
              redirectUri: `${window.location.origin}/auth/callback`,
              state: buildProviderState("feishu"),
            }
          : undefined
      }
      cliCallback={
        cliCallbackRaw && validateCliCallback(cliCallbackRaw)
          ? { url: cliCallbackRaw, state: cliState }
          : undefined
      }
      bindEmail={
        bindEmailSessionToken
          ? {
              sessionToken: bindEmailSessionToken,
              name: searchParams.get("name"),
              avatarUrl: searchParams.get("avatar_url"),
            }
          : undefined
      }
      onTokenObtained={setLoggedInCookie}
      autoStartProvider={
        provider === "feishu" ? provider : undefined
      }
      emailLogin={false}
      extra={passwordLoginForm}
      verificationCodeHint="当前测试环境默认验证码：888888"
    />
  );
}

export default function Page() {
  return (
    <Suspense fallback={null}>
      <LoginPageContent />
    </Suspense>
  );
}
