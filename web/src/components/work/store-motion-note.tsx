"use client";

import { useCallback, useEffect, useState } from "react";
import { failureText, useSession } from "@/components/work/merchant-session";
import { surfaceLabel } from "@/lib/product-finish";

interface StoreMotionParams {
  store_name: string;
  activity_time: string;
  price: string;
  address: string;
  offer_copy: string;
  cta: string;
}

interface RecordedMotion {
  status: string;
  note: string;
}

const EMPTY_PARAMS: StoreMotionParams = {
  store_name: "",
  activity_time: "",
  price: "",
  address: "",
  offer_copy: "",
  cta: "",
};

function textOf(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function recordedFrom(data: Record<string, unknown>): { params: StoreMotionParams; recorded: RecordedMotion } | null {
  const params = data.params;
  const declaration = data.declaration;
  if (!params || typeof params !== "object" || !declaration || typeof declaration !== "object") return null;
  const fields = params as Record<string, unknown>;
  const declarationFields = declaration as Record<string, unknown>;
  if (typeof fields.price !== "string") return null;
  if (typeof declarationFields.status !== "string") return null;
  if (typeof declarationFields.unchanged_store_note !== "string") return null;
  return {
    params: {
      store_name: textOf(fields.store_name),
      activity_time: textOf(fields.activity_time),
      price: fields.price,
      address: textOf(fields.address),
      offer_copy: textOf(fields.offer_copy),
      cta: textOf(fields.cta),
    },
    recorded: {
      status: declarationFields.status,
      note: declarationFields.unchanged_store_note,
    },
  };
}

export function StoreMotionNote({ campaignId }: { campaignId: string }) {
  const session = useSession();
  const [params, setParams] = useState<StoreMotionParams>(EMPTY_PARAMS);
  const [phase, setPhase] = useState<"loading" | "empty" | "ready" | "error">("loading");
  const [recorded, setRecorded] = useState<RecordedMotion | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setPhase("loading");
    const res = await session.api("GET", `campaigns/${campaignId}/store-motion`);
    if (res.status === 404) {
      setRecorded(null);
      setError("");
      setPhase("empty");
      return;
    }
    if (!res.ok) {
      setError(failureText(res.status, res.data));
      setPhase("error");
      return;
    }
    const view = recordedFrom(res.data);
    if (!view) {
      setError("没有完成（门店参数的返回不能显示）");
      setPhase("error");
      return;
    }
    setParams(view.params);
    setRecorded(view.recorded);
    setError("");
    setPhase("ready");
  }, [campaignId, session]);

  useEffect(() => {
    void load().catch(() => setPhase("error"));
  }, [load]);

  function setField(key: keyof StoreMotionParams, value: string) {
    setParams((current) => ({ ...current, [key]: value }));
  }

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const res = await session.api("PUT", `campaigns/${campaignId}/store-motion`, {
        store_name: params.store_name,
        activity_time: params.activity_time,
        price: params.price,
        address: params.address,
        offer_copy: params.offer_copy,
        cta: params.cta,
      });
      if (!res.ok) {
        setError(failureText(res.status, res.data));
        return;
      }
      const view = recordedFrom(res.data);
      if (!view) {
        setError("没有完成（门店参数的返回不能显示）");
        return;
      }
      setParams(view.params);
      setRecorded(view.recorded);
      setPhase("ready");
    } catch {
      setError("没有完成（没有连上服务）");
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      {phase === "loading" ? <p className="tk-state" data-state="loading">{surfaceLabel("loading")}</p> : null}
      {phase === "empty" ? <p className="tk-note" data-state="empty">还没有记下门店参数。</p> : null}
      {phase === "error" ? <p className="tk-state tk-danger" data-state="error">{error || surfaceLabel("error")}</p> : null}
      {recorded ? (
        <>
          <p className="tk-note" data-field="declaration-status">状态 {recorded.status}</p>
          <p className="tk-note" data-field="unchanged-store-note">{recorded.note}</p>
        </>
      ) : null}
      {error && phase !== "error" ? <p className="tk-danger">{error}</p> : null}
      {phase === "loading" ? null : (
        <form className="tk-form" onSubmit={save}>
          <label className="tk-label">
            门店名
            <input className="tk-input" name="store_name" type="text" value={params.store_name} onChange={(event) => setField("store_name", event.target.value)} required />
          </label>
          <label className="tk-label">
            活动时间
            <input className="tk-input" name="activity_time" type="text" value={params.activity_time} onChange={(event) => setField("activity_time", event.target.value)} required />
          </label>
          <label className="tk-label">
            价格
            <input className="tk-input" name="price" type="text" value={params.price} onChange={(event) => setField("price", event.target.value)} autoComplete="off" required />
          </label>
          <label className="tk-label">
            地址
            <input className="tk-input" name="address" type="text" value={params.address} onChange={(event) => setField("address", event.target.value)} required />
          </label>
          <label className="tk-label">
            优惠文案
            <input className="tk-input" name="offer_copy" type="text" value={params.offer_copy} onChange={(event) => setField("offer_copy", event.target.value)} required />
          </label>
          <label className="tk-label">
            CTA
            <input className="tk-input" name="cta" type="text" value={params.cta} onChange={(event) => setField("cta", event.target.value)} required />
          </label>
          <button className="tk-button" type="submit" disabled={saving}>记下门店参数</button>
        </form>
      )}
    </>
  );
}
