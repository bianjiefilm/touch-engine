export default function Home() {
  return (
    <main style={{ maxWidth: 720, margin: "60px auto", padding: "0 20px" }}>
      <h1>碰一碰</h1>
      <p>商家线下活动 / 内容分发独立应用(平台生态 T0)。</p>
      <ul>
        <li>
          <a href="/admin">商家后台</a>:登录、门店、活动(创建/暂停/恢复/结束)、素材引用、活动链接短码。
        </li>
        <li>
          公共活动页:<code>/c/&lt;短码&gt;</code>。游客只读,无需账户;短码即入口。
        </li>
      </ul>
      <p style={{ color: "#6b7280", fontSize: 14 }}>
        权限边界:商家后台全部走平台登录 + 租户校验;公共活动页与后台完全分离,
        不暴露后台存在性。
      </p>
    </main>
  );
}
