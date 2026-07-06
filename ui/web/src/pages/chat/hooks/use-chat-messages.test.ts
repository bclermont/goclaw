/**
 * Pure-logic tests for the event filter/handler logic in use-chat-messages.ts.
 *
 * The hook itself relies on React hooks and WebSocket connections, so we extract
 * and test the decision logic as pure functions that mirror the hook's conditions.
 */

import { describe, it, expect } from "vitest";
import type { AgentEventPayload } from "@/types/chat";

// ---------------------------------------------------------------------------
// Pure helpers that mirror the hook's filter logic
// ---------------------------------------------------------------------------

/**
 * Returns true if the event should be dropped before any processing.
 * Mirrors the two early-return guards in handleAgentEvent.
 */
function shouldDropEvent(event: AgentEventPayload, currentSessionKey: string): boolean {
  // Drop non-ws channel events that have no sessionKey and no runKind
  if (event.channel && event.channel !== "ws" && !event.runKind && !event.sessionKey) {
    return true;
  }
  // Drop events whose sessionKey doesn't match current session
  if (event.sessionKey && event.sessionKey !== currentSessionKey) {
    return true;
  }
  return false;
}

/**
 * Returns true if a run.completed event should trigger loadHistory().
 * Mirrors the unconditional reload block in handleAgentEvent.
 */
function shouldReloadOnCompleted(event: AgentEventPayload, currentSessionKey: string): boolean {
  return event.type === "run.completed" && event.sessionKey === currentSessionKey;
}

/**
 * Returns true if a run.started event should be captured (setting runIdRef).
 * Mirrors the capture condition in handleAgentEvent.
 */
function shouldCaptureRunStarted(
  event: AgentEventPayload,
  currentSessionKey: string,
  currentAgentId: string,
  expectingRun: boolean,
): boolean {
  if (event.type !== "run.started") return false;
  if (event.agentId !== currentAgentId) return false;
  return expectingRun || event.runKind === "announce" || event.sessionKey === currentSessionKey;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("event filter: shouldDropEvent", () => {
  it("does NOT drop ws-channel events", () => {
    const event: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "run-1",
      channel: "ws",
      sessionKey: "session-A",
    };
    expect(shouldDropEvent(event, "session-A")).toBe(false);
  });

  it("does NOT drop webcall events that carry a sessionKey", () => {
    const event: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "run-1",
      channel: "webcall",
      sessionKey: "session-A",
    };
    expect(shouldDropEvent(event, "session-A")).toBe(false);
  });

  it("drops non-ws events with no sessionKey and no runKind (e.g. Telegram broadcast)", () => {
    const event: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "run-1",
      channel: "telegram",
      // no sessionKey, no runKind
    };
    expect(shouldDropEvent(event, "session-A")).toBe(true);
  });

  it("drops events whose sessionKey does not match current session", () => {
    const event: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "run-1",
      channel: "ws",
      sessionKey: "session-OTHER",
    };
    expect(shouldDropEvent(event, "session-A")).toBe(true);
  });

  it("does NOT drop events with no sessionKey from ws channel (broadcast-style)", () => {
    const event: AgentEventPayload = {
      type: "chunk",
      agentId: "agent-1",
      runId: "run-1",
      channel: "ws",
      // no sessionKey
    };
    expect(shouldDropEvent(event, "session-A")).toBe(false);
  });
});

describe("run.completed — shouldReloadOnCompleted", () => {
  it("triggers loadHistory() when webcall channel event has matching sessionKey", () => {
    const event: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "webcall-run-99",
      channel: "webcall",
      sessionKey: "session-A",
    };
    expect(shouldReloadOnCompleted(event, "session-A")).toBe(true);
  });

  it("does NOT trigger loadHistory() when sessionKey mismatches", () => {
    const event: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "run-1",
      channel: "webcall",
      sessionKey: "session-B",
    };
    expect(shouldReloadOnCompleted(event, "session-A")).toBe(false);
  });

  it("does NOT trigger loadHistory() when sessionKey is absent on run.completed", () => {
    // run.completed without sessionKey cannot be matched to current session
    const event: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "run-1",
      channel: "ws",
      // no sessionKey
    };
    expect(shouldReloadOnCompleted(event, "session-A")).toBe(false);
  });

  it("does NOT trigger loadHistory() for non-completed event types", () => {
    const event: AgentEventPayload = {
      type: "run.started",
      agentId: "agent-1",
      runId: "run-1",
      channel: "webcall",
      sessionKey: "session-A",
    };
    expect(shouldReloadOnCompleted(event, "session-A")).toBe(false);
  });
});

describe("run.started — shouldCaptureRunStarted", () => {
  it("captures run.started from webcall channel when sessionKey matches", () => {
    const event: AgentEventPayload = {
      type: "run.started",
      agentId: "agent-1",
      runId: "webcall-run-42",
      channel: "webcall",
      sessionKey: "session-A",
    };
    expect(shouldCaptureRunStarted(event, "session-A", "agent-1", false)).toBe(true);
  });

  it("captures run.started when expectingRun is true (normal WS send flow)", () => {
    const event: AgentEventPayload = {
      type: "run.started",
      agentId: "agent-1",
      runId: "run-ws-1",
      channel: "ws",
      sessionKey: "session-A",
    };
    expect(shouldCaptureRunStarted(event, "session-A", "agent-1", true)).toBe(true);
  });

  it("captures run.started for delegation announce events", () => {
    const event: AgentEventPayload = {
      type: "run.started",
      agentId: "agent-1",
      runId: "run-delegate-1",
      runKind: "announce",
      channel: "ws",
    };
    expect(shouldCaptureRunStarted(event, "session-A", "agent-1", false)).toBe(true);
  });

  it("does NOT capture run.started for a different agentId", () => {
    const event: AgentEventPayload = {
      type: "run.started",
      agentId: "agent-OTHER",
      runId: "run-1",
      channel: "webcall",
      sessionKey: "session-A",
    };
    expect(shouldCaptureRunStarted(event, "session-A", "agent-1", false)).toBe(false);
  });

  it("does NOT capture run.started when not expecting and no sessionKey match", () => {
    const event: AgentEventPayload = {
      type: "run.started",
      agentId: "agent-1",
      runId: "run-1",
      channel: "telegram",
      // no sessionKey
    };
    expect(shouldCaptureRunStarted(event, "session-A", "agent-1", false)).toBe(false);
  });
});

describe("bug regression: webcall run.completed with different runId still calls loadHistory()", () => {
  /**
   * Bug scenario:
   *   1. WS send captures run.started with runId="run-ws-1" → runIdRef = "run-ws-1"
   *   2. run.started from webcall arrives with runId="webcall-run-99" (different id,
   *      same session) → mayUpdate runIdRef or not — doesn't matter for this path
   *   3. run.completed arrives from webcall with runId="webcall-run-99", sessionKey matches
   *
   * The old code gated run.completed on runId matching runIdRef, so if runIdRef was
   * still "run-ws-1", webcall run.completed (with "webcall-run-99") was silently dropped.
   *
   * The fix: run.completed is handled UNCONDITIONALLY for the current session,
   * before the runId gate — so shouldReloadOnCompleted() only needs sessionKey.
   */
  it("run.completed from webcall triggers reload regardless of what runId was previously captured", () => {
    const capturedRunId = "run-ws-1"; // what the UI captured from WS run.started
    const webcallCompletedEvent: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "webcall-run-99", // different from captured runId
      channel: "webcall",
      sessionKey: "session-A",
    };
    const currentSession = "session-A";

    // First: event must not be dropped
    expect(shouldDropEvent(webcallCompletedEvent, currentSession)).toBe(false);

    // Second: reload must be triggered regardless of capturedRunId
    const runIdGateWouldBlock = webcallCompletedEvent.runId !== capturedRunId;
    expect(runIdGateWouldBlock).toBe(true); // confirm the old bug would have blocked it

    // The fix bypasses the runId gate entirely for run.completed on current session
    expect(shouldReloadOnCompleted(webcallCompletedEvent, currentSession)).toBe(true);
  });

  it("does not reload for a webcall run.completed on a different session", () => {
    const webcallCompletedEvent: AgentEventPayload = {
      type: "run.completed",
      agentId: "agent-1",
      runId: "webcall-run-99",
      channel: "webcall",
      sessionKey: "session-B",
    };
    // First: dropped because sessionKey mismatch
    expect(shouldDropEvent(webcallCompletedEvent, "session-A")).toBe(true);
    // Even without the drop, reload should not trigger
    expect(shouldReloadOnCompleted(webcallCompletedEvent, "session-A")).toBe(false);
  });
});
