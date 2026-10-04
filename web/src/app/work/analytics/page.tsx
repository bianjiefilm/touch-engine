"use client";

import { useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";

interface Metric {
  key: string;
  title: string;
  available: boolean;
  value?: number | null;
  reason?: string;
}

export default function AnalyticsPage() {
  const session = useSession();
  const [phase, setPhase] = useState<"loading" | "error" | "empty" | "ready">("loading");
  const [error, setError] = useState("");
  const [metrics, setMetrics] = useState<Metric[]>([]);

  useEffect(() => {
    let alive = true;
    const now = new Date();
    const start = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0);
    const params = new URLSearchParams({
      window_start: toRfc3339(start),
      window_end: toRfc3339(now),
    });
    void session.api("GET", `dashboard?${params.toString()}`).then((res) => {
      if (!alive) return;
      if (res.status === 404) {
        setPhase("empty");
        setError("统计还没开通。缺失保持未知，不显示 0。");
        return;
      }
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        setPhase("error");
        return;
      }
      const items = Array.isArray(res.data.metrics) ? (res.data.metrics as Metric[]) : [];
      setMetrics(items);
      setPhase(items.length === 0 ? "empty" : "ready");
    }).catch(() => {
      if (alive) setPhase("error");
    });
    return () => {
      alive = false;
    };
  }, [session]);

  return (
    <main>
      <h1 className="tk-title">今天的统计</h1>
      <p className="tk-lead">只显示已经发生的碰、扫码、留资和接收。外部播放、发布和核销拿不到就是未知。</p>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{error || surfaceLabel("error")}</p> : null}
      {phase === "empty" ? <p className="tk-state" data-state="empty">{error || "这个窗口还没有可读的统计。未知不写成 0。"}</p> : null}
      {phase === "ready" ? (
        <ul className="tk-list">
          {metrics.map((metric) => (
            <li key={metric.key}>
              <strong>{metric.title}</strong>
              {metric.available && typeof metric.value === "number" ? (
                <span> {metric.value}</span>
              ) : (
                <span className="tk-unknown"> 未知{metric.reason ? `。${metric.reason}` : ""}</span>
              )}
            </li>
          ))}
        </ul>
      ) : null}
      <p className="tk-gap"><a href="/work/rewards">去看奖励和核销为什么还是未知</a></p>
    </main>
  );
}

function toRfc3339(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, "0");
  const offset = -date.getTimezoneOffset();
  const sign = offset >= 0 ? "+" : "-";
  const abs = Math.abs(offset);
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`;
}
