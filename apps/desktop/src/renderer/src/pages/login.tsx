import { LoginPage } from "@multica/views/auth";
import { DragStrip } from "@multica/views/platform";
import { MulticaIcon } from "@multica/ui/components/common/multica-icon";

function requireRuntimeAppUrl(): string {
  const runtimeConfig = window.desktopAPI.runtimeConfig;
  if (!runtimeConfig.ok) {
    throw new Error(
      "Invariant violated: DesktopLoginPage rendered before App accepted runtime config",
    );
  }
  return runtimeConfig.config.appUrl;
}

export function DesktopLoginPage() {
  const webUrl = requireRuntimeAppUrl();
  const handleGoogleLogin = () => {
    // Open the hosted login page in the default browser. The callback page
    // will redirect back via multica:// deep link with the token.
    window.desktopAPI.openExternal(
      `${webUrl}/login?platform=desktop&provider=google`,
    );
  };

  const handleFeishuLogin = () => {
    window.desktopAPI.openExternal(
      `${webUrl}/login?platform=desktop&provider=feishu`,
    );
  };

  return (
    <div className="flex h-screen flex-col">
      <DragStrip />
      <LoginPage
        logo={<MulticaIcon bordered size="lg" />}
        onSuccess={() => {
          // Auth store update triggers AppContent re-render → shows DesktopShell.
          // Initial workspace navigation happens in routes.tsx via IndexRedirect.
        }}
        onGoogleLogin={handleGoogleLogin}
        onFeishuLogin={handleFeishuLogin}
      />
    </div>
  );
}
