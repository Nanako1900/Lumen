import { Link } from "react-router-dom";
import PageSection from "../components/PageSection";

export default function NotFound() {
  return (
    <PageSection title="页面未找到" subtitle="你访问的页面不存在或已移动。">
      <Link
        to="/"
        className="inline-flex items-center justify-center rounded-xl bg-indigo-600 px-5 py-2.5 font-semibold text-white transition-colors hover:bg-indigo-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400"
      >
        返回首页
      </Link>
    </PageSection>
  );
}
