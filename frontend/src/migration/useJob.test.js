import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { JOB_POLL_MS, useJob } from "./useJob.js";

const { getJob } = vi.hoisted(() => ({ getJob: vi.fn() }));
vi.mock("./jobs.js", async (original) => ({ ...await original(), getJob }));
const flush = () => act(async () => {});
const advance = (ms = JOB_POLL_MS) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });
beforeEach(() => { vi.useFakeTimers(); getJob.mockReset(); });
afterEach(() => { vi.useRealTimers(); });

describe("job polling", () => {
  it("polls queued, running and active partial until completion", async () => {
    getJob.mockResolvedValueOnce({ status: "queued" }).mockResolvedValueOnce({ status: "running" })
      .mockResolvedValueOnce({ status: "partial", paused: false }).mockResolvedValueOnce({ status: "completed" });
    const { result } = renderHook(() => useJob("db1", "job1"));
    await flush();
    expect(result.current.job.status).toBe("queued");
    await advance(); await advance(); await advance();
    expect(result.current.job.status).toBe("completed");
    await advance(JOB_POLL_MS * 3);
    expect(getJob).toHaveBeenCalledTimes(4);
  });
  it.each([{ status: "failed" }, { status: "partial", paused: true }, { status: "unresolved" }, { status: "reverted" }, { status: "partial", paused: true, compensation_status: "partially_reverted" }])("stops at a waiting or terminal state %j", async (job) => {
    getJob.mockResolvedValue(job);
    renderHook(() => useJob("db1", "job1"));
    await flush(); await advance(JOB_POLL_MS * 3);
    expect(getJob).toHaveBeenCalledTimes(1);
  });
  it("cleans up polling on unmount", async () => {
    getJob.mockResolvedValue({ status: "running" });
    const { unmount } = renderHook(() => useJob("db1", "job1"));
    await flush(); unmount(); await advance(JOB_POLL_MS * 3);
    expect(getJob).toHaveBeenCalledTimes(1);
  });
  it("backs off after read errors and stops after three consecutive failures", async () => {
    getJob.mockRejectedValue(new Error("network error"));
    const { result } = renderHook(() => useJob("db1", "job1"));
    await flush(); await advance(JOB_POLL_MS); expect(getJob).toHaveBeenCalledTimes(1);
    await advance(JOB_POLL_MS); await advance(JOB_POLL_MS * 4);
    expect(result.current.error).toBe("network error");
    await advance(JOB_POLL_MS * 10); expect(getJob).toHaveBeenCalledTimes(3);
  });
});
