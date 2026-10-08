"use client";

import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { agentWorkBanner, commitTenantSwitch, payerLabel } from "@/lib/eco-nav/touch-shell";
import {
  billingBadgeText,
  LAUNCH_UNRESOLVED_RESIDUAL,
  partitionApps,
  payerText,
  pinTouchApp,
  planManualSwitch,
  resolveTargetHref,
  selectTenant,
  switchableApps,
  type EcoNavModel,
  type EcoNavViewport,
  type LaunchResolver,
  type VisibleApp,
} from "@/lib/eco-nav/model";
import styles from "./eco-top-nav.module.css";

function useNarrow(enabled: boolean): boolean {
  const [narrow, setNarrow] = useState(false);
  useEffect(() => {
    if (!enabled || typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(max-width: 1100px)");
    const apply = () => setNarrow(media.matches);
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [enabled]);
  return narrow;
}

function AppControl({
  app,
  canSwitch,
  resolve,
  inMenu = false,
}: {
  app: VisibleApp;
  canSwitch: boolean;
  resolve: LaunchResolver;
  inMenu?: boolean;
}) {
  const plan = planManualSwitch(app, canSwitch, resolve);
  const role = inMenu ? "menuitem" : undefined;
  if (!plan.href) {
    return (
      <span
        className={styles.quiet}
        role={role}
        data-testid={`eco-app-${app.app_id}`}
        data-nav-intent="manual_switch"
        data-creates-handoff="false"
        data-launch-mode={app.launch_mode}
        data-launch-target-id={app.launch_target_id ?? ""}
        data-launch-residual="unresolved_launch_target"
        title={plan.residual ?? LAUNCH_UNRESOLVED_RESIDUAL}
      >
        {app.display_name}
      </span>
    );
  }
  return (
    <a
      className={styles.link}
      role={role}
      href={plan.href}
      data-testid={`eco-app-${app.app_id}`}
      data-nav-intent={plan.nav_intent}
      data-creates-handoff="false"
      data-launch-mode={app.launch_mode}
      data-launch-target-id={app.launch_target_id ?? ""}
      data-icon-ref={app.icon_ref ?? ""}
    >
      {app.display_name}
    </a>
  );
}

const noopResolve: LaunchResolver = () => null;

export function EcoTopNav({
  model,
  nickname,
  sessionRole,
  viewport,
  resolveLaunchTarget = noopResolve,
  onTenantChange,
  onLogout,
  honorScopeMerchant = false,
}: {
  model: EcoNavModel;
  nickname: string;
  sessionRole: string;
  viewport?: EcoNavViewport;
  resolveLaunchTarget?: LaunchResolver;
  onTenantChange?: (tenantId: string) => void;
  onLogout?: () => void;
  /** 仅预览页打开。商家后台不把夹具租户写进会话。 */
  honorScopeMerchant?: boolean;
}) {
  const [nav, setNav] = useState(() => pinTouchApp(model));
  const [menuOpen, setMenuOpen] = useState(false);
  const [userOpen, setUserOpen] = useState(false);
  const overflowRef = useRef<HTMLButtonElement>(null);
  const userRef = useRef<HTMLButtonElement>(null);
  const autoNarrow = useNarrow(viewport === undefined);
  const narrow = viewport ? viewport === "narrow" : autoNarrow;

  useEffect(() => {
    setNav(pinTouchApp(model));
    setMenuOpen(false);
    setUserOpen(false);
  }, [model]);

  if (!nav.renderable || !nav.brand) return null;

  const apps = switchableApps(nav.apps, nav.current_app.app_id);
  const parts = partitionApps(apps, narrow ? "narrow" : "wide");
  const canSwitch = nav.capabilities.can_switch_app === true;
  const returnHref = nav.return_context
    ? resolveTargetHref(true, nav.return_context.return_target_id, resolveLaunchTarget)
    : null;
  const billingHref = resolveTargetHref(nav.capabilities.can_open_billing, nav.billing?.entry ?? null, resolveLaunchTarget);
  const unresolved = [...parts.pinned, ...parts.overflow].some(
    (app) => planManualSwitch(app, canSwitch, resolveLaunchTarget).href === null,
  );
  const scopeTrusted = honorScopeMerchant || nav.provenance === "public_ai_context";
  const payer = scopeTrusted ? payerText(nav) : payerLabel(nav.provenance, payerText(nav));
  const merchant = nav.scopes.find((item) => item.tenant_id === nav.active_tenant_id)?.display_name ?? null;
  const banner = agentWorkBanner(sessionRole, scopeTrusted ? merchant : null);

  function chooseTenant(tenantId: string) {
    setNav((current) => {
      const next = selectTenant(current, tenantId);
      if (next.active_tenant_id === current.active_tenant_id) return next;
      const committed = honorScopeMerchant
        ? next.active_tenant_id
        : commitTenantSwitch(next.provenance, next.active_tenant_id);
      if (committed) onTenantChange?.(committed);
      return next;
    });
  }

  function onKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (event.key !== "Escape") return;
    if (!menuOpen && !userOpen) return;
    if (menuOpen) overflowRef.current?.focus();
    else userRef.current?.focus();
    setMenuOpen(false);
    setUserOpen(false);
  }

  return (
    <header
      className={styles.bar}
      aria-label="生态导航"
      data-testid="eco-top-nav"
      data-viewport={narrow ? "narrow" : "wide"}
      data-provenance={nav.provenance}
      data-theme={nav.brand.theme.token_set_ref}
      onKeyDown={onKeyDown}
    >
      <div className={styles.brand} data-testid="eco-brand">
        <span className={styles.brandName} title={nav.brand.display_name}>
          {nav.brand.display_name}
        </span>
        <span
          className={styles.currentApp}
          data-testid="eco-current-app"
          data-app-id={nav.current_app.app_id}
          aria-current="page"
          title={nav.current_app.display_name}
        >
          {nav.current_app.display_name}
        </span>
      </div>

      <div className={styles.apps}>
        <div className={styles.pinned} data-testid="eco-app-pinned">
          {parts.pinned.map((app) => (
            <AppControl key={app.app_id} app={app} canSwitch={canSwitch} resolve={resolveLaunchTarget} />
          ))}
        </div>
        {parts.overflow.length > 0 ? (
          <div className={styles.popover}>
            <button
              ref={overflowRef}
              type="button"
              className={styles.button}
              aria-expanded={menuOpen}
              aria-haspopup="menu"
              onClick={() => setMenuOpen((open) => !open)}
            >
              应用
            </button>
            {menuOpen ? (
              <div className={`${styles.menu} ${styles.menuLeft}`} role="menu" data-testid="eco-app-menu">
                {parts.overflow.map((app) => (
                  <AppControl key={app.app_id} app={app} canSwitch={canSwitch} resolve={resolveLaunchTarget} inMenu />
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
        {unresolved ? (
          <p className={styles.srOnly} data-testid="eco-launch-residual">
            {LAUNCH_UNRESOLVED_RESIDUAL}
          </p>
        ) : null}
      </div>

      {nav.return_context ? (
        returnHref ? (
          <a
            className={styles.link}
            data-testid="eco-return-chip"
            href={returnHref}
            data-nav-intent="return_source"
            data-creates-handoff="false"
            data-launch-target-id={nav.return_context.return_target_id}
          >
            {nav.return_context.label}
          </a>
        ) : (
          <span
            className={styles.quiet}
            data-testid="eco-return-chip"
            data-nav-intent="return_source"
            data-creates-handoff="false"
            data-launch-residual="unresolved_launch_target"
            data-launch-target-id={nav.return_context.return_target_id}
            title={LAUNCH_UNRESOLVED_RESIDUAL}
          >
            {nav.return_context.label}
          </span>
        )
      ) : null}

      <div className={styles.spacer} />

      <div className={styles.context} data-testid="eco-nav-context">
        {banner ? (
          <span data-testid="eco-agent-banner" title={banner}>
            {banner}
          </span>
        ) : null}
        {scopeTrusted && nav.capabilities.can_switch_tenant && nav.scopes.length > 1
          ? nav.scopes.map((item) => {
              const name = item.display_name ?? item.tenant_id;
              return (
                <button
                  key={item.tenant_id}
                  type="button"
                  className={item.tenant_id === nav.active_tenant_id ? `${styles.button} ${styles.pressed}` : styles.button}
                  data-testid={`eco-tenant-${item.tenant_id}`}
                  aria-pressed={item.tenant_id === nav.active_tenant_id}
                  title={name}
                  onClick={() => chooseTenant(item.tenant_id)}
                >
                  <span className={styles.ellipsis}>{name}</span>
                </button>
              );
            })
          : null}
        <span className={styles.ellipsis} data-testid="eco-payer" title={payer}>
          {payer === "付款主体需确认" ? payer : `付款主体：${payer}`}
        </span>
        <span data-testid="eco-billing">{billingBadgeText(nav)}</span>
        {nav.provenance !== "public_ai_context" ? (
          <span className={styles.badge} data-testid="eco-provenance">
            {nav.status.summary || "预览上下文 · 未接公共身份"}
          </span>
        ) : null}
      </div>

      <div className={styles.right}>
        <div className={styles.popover}>
          <button
            ref={userRef}
            type="button"
            className={styles.button}
            aria-haspopup="menu"
            aria-expanded={userOpen}
            aria-label={`账户 ${nickname}`}
            onClick={() => setUserOpen((open) => !open)}
          >
            <span className={styles.ellipsis}>{nickname}</span>
          </button>
          {userOpen ? (
            <div className={styles.menu} role="menu">
              <p className={styles.roleLine}>{nav.role_label || "角色需确认"}</p>
              {billingHref ? (
                <a className={styles.link} data-testid="eco-billing-link" href={billingHref} data-creates-handoff="false">
                  账单与额度
                </a>
              ) : null}
              {onLogout ? (
                <button type="button" className={styles.button} onClick={onLogout}>
                  退出
                </button>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </header>
  );
}
