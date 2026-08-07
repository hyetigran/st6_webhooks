import { Navigate, Route, Routes } from "react-router-dom";
import { Landing } from "./pages/Landing";
import { ConsoleShell } from "./pages/console/ConsoleShell";
import { Endpoints } from "./pages/console/Endpoints";

export function App() {
  return (
    <Routes>
      <Route path="/" element={<Landing />} />
      <Route path="/console" element={<ConsoleShell />}>
        <Route index element={<Navigate to="endpoints" replace />} />
        <Route path="endpoints" element={<Endpoints />} />
      </Route>
    </Routes>
  );
}
