import HeroSection from "../components/home/HeroSection";
import StatsBar from "../components/home/StatsBar";
import FeatureGrid from "../components/home/FeatureGrid";
import SelfHostSection from "../components/home/SelfHostSection";
import FinalCta from "../components/home/FinalCta";

/** 首页：Hero + 数据条 + 功能卡 + 自托管演示 + 收尾 CTA（设计稿 1a）。 */
export default function Home() {
  return (
    <>
      <HeroSection />
      <StatsBar />
      <FeatureGrid />
      <SelfHostSection />
      <FinalCta />
    </>
  );
}
