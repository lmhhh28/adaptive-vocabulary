import React from "react";
import { ImportsView } from "./ImportsView";

export interface AppModule {
  id: string;
  route: string;
  title: string;
  Component: React.ComponentType<{ onNavigate?: (path: string) => void }>;
}

export const importsModule: AppModule = {
  id: "W05",
  route: "/imports",
  title: "词表导入与消歧",
  Component: ImportsView,
};

export default importsModule;
