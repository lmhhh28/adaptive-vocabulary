import React, { useState, useEffect } from "react";
import { BankListView } from "./BankListView";
import { BankDetailView } from "./BankDetailView";

interface BanksViewProps {
  onNavigate?: (path: string) => void;
  currentPath?: string;
}

export const BanksView: React.FC<BanksViewProps> = ({ onNavigate, currentPath: propPath }) => {
  const [internalPath, setInternalPath] = useState<string>(
    propPath || window.location.pathname || "/banks"
  );

  useEffect(() => {
    if (propPath) {
      setInternalPath(propPath);
    }
  }, [propPath]);

  useEffect(() => {
    const handlePop = () => {
      setInternalPath(window.location.pathname || "/banks");
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

  const cleanPath = currentPath.split("?")[0];
  const trimmed = cleanPath.replace(/^\/+|\/+$/g, "");
  const parts = trimmed.split("/");

  if (parts.length <= 1 || parts[1] === "") {
    return <BankListView onNavigate={handleNavigate} />;
  }

  const bankId = parts[1];
  return <BankDetailView bankId={bankId} onNavigate={handleNavigate} />;
};
