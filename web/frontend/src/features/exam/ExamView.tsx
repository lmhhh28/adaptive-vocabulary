import React, { useState, useEffect } from "react";
import { ExamCreateView } from "./ExamCreateView";
import { ExamActiveView } from "./ExamActiveView";
import { ExamResultView } from "./ExamResultView";

interface ExamViewProps {
  onNavigate?: (path: string) => void;
  currentPath?: string;
}

export const ExamView: React.FC<ExamViewProps> = ({ onNavigate, currentPath: propPath }) => {
  const [internalPath, setInternalPath] = useState<string>(
    propPath || window.location.pathname || "/exams/new"
  );

  useEffect(() => {
    if (propPath) {
      setInternalPath(propPath);
    }
  }, [propPath]);

  useEffect(() => {
    const handlePop = () => {
      setInternalPath(window.location.pathname || "/exams/new");
    };
    window.addEventListener("popstate", handlePop);
    return () => window.removeEventListener("popstate", handlePop);
  }, []);

  const currentPath = propPath || internalPath;

  const handleNavigate = (path: string) => {
    if (window.location.pathname !== path) {
      window.history.pushState({}, "", path);
    }
    setInternalPath(path);
    onNavigate?.(path);
  };

  // Route matching:
  // /exams or /exams/new -> ExamCreateView
  // /exams/:id/result -> ExamResultView
  // /exams/:id -> ExamActiveView
  const cleanPath = currentPath.split("?")[0];
  const trimmed = cleanPath.replace(/^\/+|\/+$/g, "");
  const parts = trimmed.split("/");

  if (parts.length <= 1 || parts[1] === "new" || parts[1] === "") {
    return <ExamCreateView onNavigate={handleNavigate} />;
  }

  const examId = parts[1];
  if (parts.length >= 3 && parts[2] === "result") {
    return <ExamResultView examId={examId} onNavigate={handleNavigate} />;
  }

  return <ExamActiveView examId={examId} onNavigate={handleNavigate} />;
};
