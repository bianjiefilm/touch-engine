import { notFound } from "next/navigation";
import { PreviewShell } from "./preview-shell";

export default function EcoNavPreviewPage() {
  if (process.env.TOUCH_ECO_NAV_PREVIEW !== "1") notFound();
  return <PreviewShell />;
}
