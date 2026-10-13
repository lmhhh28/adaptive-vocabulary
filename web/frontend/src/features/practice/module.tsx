import React from "react";
import { PracticeView } from "./PracticeView";

export interface AppModule {
  id: string;
  route: string;
  title: string;
  Component: React.ComponentType<{ onNavigate?: (path: string) => void }>;
}

export const practiceModule: AppModule = {
  id: "W02",
  route: "/practice",
  title: "自适应练习",
  Component: PracticeView,
};

export default practiceModule;
