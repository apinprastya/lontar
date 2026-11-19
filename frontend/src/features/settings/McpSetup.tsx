import { useEffect, useState } from "react";
import { copyText } from "../../lib/clipboard";
import { errorMessage } from "../../lib/errors";
import { settingsApi, type MCPSetup } from "./api";

/** One copyable snippet. */
function Snippet({
  label,
  text,
  multiline,
}: {
  label: string;
  text: string;
  multiline?: boolean;
}) {
  const [status, setStatus] = useState("");
  const copy = async () => {
    try {
      await copyText(text);
      setStatus("Copied");
    } catch (err) {
      setStatus(`Copy failed: ${errorMessage(err)}`);
    }
  };
  return (
    <div className="mcp-snippet">
      {multiline ? (
        <pre className="mono" aria-label={label}>
          {text}
        </pre>
      ) : (
        <code className="mono" aria-label={label}>
          {text}
        </code>
      )}
      <button
        type="button"
        onClick={() => void copy()}
        aria-label={`Copy ${label}`}
      >
        Copy
      </button>
      <span className="hint" aria-live="polite">
        {status}
      </span>
    </div>
  );
}

/**
 * Settings section explaining how to connect Claude to `lontar mcp`, with
 * the commands for this installation.
 */
export default function McpSetupSection() {
  const [setup, setSetup] = useState<MCPSetup | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    settingsApi
      .mcpSetup()
      .then((s) => {
        if (!live) return;
        if (s.error) setError(s.error);
        else setSetup(s);
      })
      .catch((err) => live && setError(errorMessage(err)));
    return () => {
      live = false;
    };
  }, []);

  return (
    <div className="mcp-setup">
      <p className="hint">
        Let Claude list, explore and query the connections you enable: edit a
        connection and set <strong>AI access (MCP)</strong>. Changes need your
        approval in Claude, and query results are sent to the AI provider.
      </p>
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      {setup && (
        <>
          <span className="mcp-label">Claude Code: run in a terminal</span>
          <Snippet label="Claude Code command" text={setup.claudeCode} />
          <span className="mcp-label">
            Claude Desktop: add to claude_desktop_config.json
          </span>
          <Snippet
            label="Claude Desktop configuration"
            text={setup.claudeDesktop}
            multiline
          />
        </>
      )}
    </div>
  );
}
