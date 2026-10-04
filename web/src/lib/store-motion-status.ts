// 状态字符串来自服务端，页面不能照画：只有唯一已知的未成片状态可以显示
// 原文，其余一律未知语气。任何 tone 都不是成功。
export interface DeclarationPresentation {
  tone: "pending" | "unknown";
  copy: string;
}

const PENDING = "还没生成成片";

export function presentDeclaration(raw: string | undefined): DeclarationPresentation {
  if (raw === PENDING) {
    return { tone: "pending", copy: PENDING };
  }
  if (raw === undefined || raw === "") {
    return { tone: "unknown", copy: "状态未知（服务端还没给出声明）" };
  }
  return { tone: "unknown", copy: "状态未知（出现未识别状态）" };
}
