import { createApp } from "./app.js";
import { server } from "./config.js";

const app = createApp();
app.listen(server.port, () => {
  console.log(`API listening on :${server.port}`);
});
