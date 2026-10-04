import type { ReactNode } from "react";

// HUI-2628 r2（裁决4）：admin 面挂 compact surface。
// tokens.css/aliases.css 都是 [data-pn-surface][data-pn-theme] 显式作用域，
// 本元素是子树 --pn-*/--tk-* 的作用域祖先。
export default function AdminLayout({ children }: { children: ReactNode }) {
  return <div data-pn-surface="admin.compact" data-pn-theme="light">{children}</div>;
}
