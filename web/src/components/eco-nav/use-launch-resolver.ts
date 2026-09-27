"use client";

import { useEffect, useState } from "react";
import type { LaunchResolver } from "@/lib/eco-nav/model";

export function useLaunchResolver(): LaunchResolver {
  const [map, setMap] = useState<Record<string, string>>({});
  useEffect(() => {
    let alive = true;
    fetch("/api/eco-nav/launches")
      .then(async (res) => {
        if (!res.ok) throw new Error("launches");
        return (await res.json()) as Record<string, string>;
      })
      .then((next) => {
        if (alive) setMap(next);
      })
      .catch(() => {
        if (alive) setMap({});
      });
    return () => {
      alive = false;
    };
  }, []);
  return (targetId) => map[targetId] ?? null;
}
