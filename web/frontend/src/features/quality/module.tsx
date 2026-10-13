import React from "react";
import { QualityView } from "./QualityView";

export interface AppModule {
  id: string;
  route: string;
  title: string;
  Component: React.ComponentType<{ onNavigate?: (path: string) => void }>;
}

export const qualityModule: AppModule = {
  id: "W07",
  route: "/quality",
  title: "质量处置、学习报告与维护",
  Component: QualityView,
};

export default qualityModule;
