import React from "react";
import { GenerationView } from "./GenerationView";

export interface AppModule {
  id: string;
  route: string;
  title: string;
  Component: React.ComponentType<{ onNavigate?: (path: string) => void }>;
}

export const generationModule: AppModule = {
  id: "W06",
  route: "/generation",
  title: "内容生成与质检",
  Component: GenerationView,
};

export default generationModule;
