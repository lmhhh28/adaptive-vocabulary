import React from "react";
import { BanksView } from "./BanksView";

export interface AppModule {
  id: string;
  route: string;
  title: string;
  Component: React.ComponentType<{ onNavigate?: (path: string) => void }>;
}

export const bankModule: AppModule = {
  id: "W04",
  route: "/banks",
  title: "题库与内容",
  Component: BanksView,
};

export default bankModule;
