import React from "react";
import { ExamView } from "./ExamView";

export interface AppModule {
  id: string;
  route: string;
  title: string;
  Component: React.ComponentType<{ onNavigate?: (path: string) => void }>;
}

export const examModule: AppModule = {
  id: "W03",
  route: "/exams",
  title: "阶段考试",
  Component: ExamView,
};

export default examModule;
