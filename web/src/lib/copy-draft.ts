// HUI-1668: the merchant UI must not call an unauthorized draft "usable",
// and the professional-tool handoff must not invent a launch URL.

export interface CopyUsabilityInput {
  usable: boolean;
  model_status: string;
  billed: boolean;
}

export function copyUsability(draft: CopyUsabilityInput): { usable: boolean; label: string } {
  if (draft.billed) {
    return { usable: false, label: "这次没有完成扣费，也不能把结果展示成已扣费文案。" };
  }
  if (draft.model_status !== "authorized" || !draft.usable) {
    return { usable: false, label: "这不是真实可用文案。模型未授权，或输出没有通过已确认资料核对。" };
  }
  return { usable: true, label: "模型已授权，标题通过已确认资料核对。这仍是草稿，尚未发布。" };
}

const blockedHost = (value: string) => {
  try {
    const url = new URL(value);
    const host = url.hostname.toLowerCase();
    return host === "localhost" || host === "127.0.0.1" || host === "::1" || host === "invalid" || host.endsWith(".invalid");
  } catch {
    return value.includes(".invalid") || value.includes("localhost") || value.includes("127.0.0.1");
  }
};

export function handoffHasFormalJump(handoff: Record<string, unknown>): boolean {
  for (const value of Object.values(handoff)) {
    if (typeof value === "string" && /^https?:\/\//i.test(value) && blockedHost(value)) {
      return true;
    }
  }
  return false;
}
