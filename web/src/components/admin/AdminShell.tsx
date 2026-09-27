"use client";

import { useEffect, useState, type ReactNode } from "react";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { provisionalEcoNav } from "@/lib/eco-nav/fixture";
import type { EcoNavModel } from "@/lib/eco-nav/model";

export function AdminShell({
  children,
  nickname,
  sessionRole,
  onLogout,
  onTenantChange,
}: {
  children: ReactNode;
  nickname: string;
  sessionRole: string;
  onLogout: () => void;
  onTenantChange: (tenantId: string) => void;
}) {
  const [model, setModel] = useState<EcoNavModel | null>(null);

  useEffect(() => {
    let alive = true;
    fetch("/api/eco-nav")
      .then(async (res) => {
        if (!res.ok) throw new Error("eco-nav");
        return (await res.json()) as EcoNavModel;
      })
      .then((next) => {
        if (alive) setModel(next);
      })
      .catch(() => {
        if (alive) setModel(provisionalEcoNav());
      });
    return () => {
      alive = false;
    };
  }, []);

  return (
    <>
      {model ? (
        <EcoTopNav
          model={model}
          nickname={nickname}
          sessionRole={sessionRole}
          onLogout={onLogout}
          onTenantChange={onTenantChange}
        />
      ) : null}
      {children}
    </>
  );
}
