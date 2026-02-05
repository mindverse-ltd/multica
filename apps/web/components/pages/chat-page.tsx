"use client";

import { useRef } from "react";
import { Header } from "@/app/header";
import { Button } from "@multica/ui/components/ui/button";
import { Loading } from "@multica/ui/components/ui/loading";
import { ChatInput } from "@multica/ui/components/chat-input";
import { MessageList } from "@multica/ui/components/message-list";
import { useScrollFade } from "@multica/ui/hooks/use-scroll-fade";
import { useAutoScroll } from "@multica/ui/hooks/use-auto-scroll";
import { useGatewayConnection } from "@/hooks/use-gateway-connection";
import { useChat } from "@/hooks/use-chat";
import { ExecApprovalItem } from "@multica/ui/components/exec-approval-item";
import { DevicePairing } from "@/components/device-pairing";

const ChatPage = () => {
  const { pageState, connectionState, identity, error, client, pairingKey, connect, disconnect } =
    useGatewayConnection();

  return (
    <div className="h-full flex flex-col bg-background">
      <Header />
      <div className="flex-1 overflow-hidden min-h-0 flex flex-col">
        {pageState === "loading" && (
          <div className="flex-1 flex items-center justify-center gap-2 text-muted-foreground text-sm">
            <Loading />
            Loading...
          </div>
        )}

        {(pageState === "not-connected" || pageState === "connecting") && (
          <DevicePairing
            key={pairingKey}
            connectionState={connectionState}
            lastError={error}
            onConnect={connect}
            onCancel={disconnect}
          />
        )}

        {pageState === "connected" && client && identity && (
          <ChatView
            client={client}
            hubId={identity.hubId}
            agentId={identity.agentId}
            onDisconnect={disconnect}
          />
        )}
      </div>
    </div>
  );
};

function ChatView({
  client,
  hubId,
  agentId,
  onDisconnect,
}: {
  client: NonNullable<ReturnType<typeof useGatewayConnection>["client"]>;
  hubId: string;
  agentId: string;
  onDisconnect: () => void;
}) {
  const { messages, streamingIds, isLoading, error, pendingApprovals, sendMessage, resolveApproval } = useChat({ client, hubId, agentId });

  const mainRef = useRef<HTMLElement>(null);
  const fadeStyle = useScrollFade(mainRef);
  useAutoScroll(mainRef);

  return (
    <div className="flex-1 flex flex-col min-h-0">
      <div className="container flex items-center justify-end p-2">
        <button
          onClick={onDisconnect}
          className="text-xs text-muted-foreground hover:text-foreground"
        >
          Disconnect
        </button>
      </div>

      <main ref={mainRef} className="flex-1 overflow-y-auto min-h-0" style={fadeStyle}>
        {messages.length === 0 && pendingApprovals.length === 0 ? (
          <div className="flex items-center justify-center h-full text-muted-foreground text-sm">
            Your Agent is ready
          </div>
        ) : (
          <>
            <MessageList messages={messages} streamingIds={streamingIds} />
            {pendingApprovals.length > 0 && (
              <div className="relative px-4 max-w-4xl mx-auto">
                {pendingApprovals.map((approval) => (
                  <ExecApprovalItem
                    key={approval.approvalId}
                    command={approval.command}
                    cwd={approval.cwd}
                    riskLevel={approval.riskLevel}
                    riskReasons={approval.riskReasons}
                    expiresAtMs={approval.expiresAtMs}
                    onDecision={(decision) => resolveApproval(approval.approvalId, decision)}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </main>

      {error && (
        <div className="container px-4" role="alert" aria-live="polite">
          <div className="rounded-lg bg-destructive/5 border border-destructive/15 text-xs px-3 py-2 flex items-center justify-between gap-3">
            <span className="text-foreground leading-snug">{error.message}</span>
            <Button
              variant="destructive"
              size="sm"
              onClick={onDisconnect}
              className="shrink-0 text-xs h-7 px-2.5"
            >
              Disconnect
            </Button>
          </div>
        </div>
      )}

      <footer className="container p-2 pt-1">
        <ChatInput
          onSubmit={sendMessage}
          disabled={isLoading || !!error}
          placeholder={error ? "Connection error" : "Ask your Agent..."}
        />
      </footer>
    </div>
  );
}

export default ChatPage;
