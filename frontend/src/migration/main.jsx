import React from "react";
import { createRoot } from "react-dom/client";
import { MigrationApp } from "./MigrationApp.jsx";
import "../index.css";
import "./style.css";
createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <MigrationApp />
  </React.StrictMode>,
);
