import { Navigate, Route, Routes } from "react-router-dom";
import { Landing } from "./pages/Landing";
import { ConsoleShell } from "./pages/console/ConsoleShell";
import { DeliveryDetail } from "./pages/console/DeliveryDetail";
import { EndpointDetail } from "./pages/console/EndpointDetail";
import { Endpoints } from "./pages/console/Endpoints";
import { EventDetail } from "./pages/console/EventDetail";
import { Events } from "./pages/console/Events";

export function App() {
  return (
    <Routes>
      <Route path="/" element={<Landing />} />
      <Route path="/console" element={<ConsoleShell />}>
        <Route index element={<Navigate to="endpoints" replace />} />
        <Route path="endpoints" element={<Endpoints />} />
        <Route path="endpoints/:id" element={<EndpointDetail />} />
        <Route path="events" element={<Events />} />
        <Route path="events/:id" element={<EventDetail />} />
        <Route path="deliveries/:id" element={<DeliveryDetail />} />
      </Route>
    </Routes>
  );
}
