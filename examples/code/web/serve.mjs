// Serves the packed intro, dist/websqz-output, on http://127.0.0.1:8080: the
// packed page reads itself with fetch, which browsers do not allow from
// file://.
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const dir = join(dirname(fileURLToPath(import.meta.url)), "dist", "websqz-output");
createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/html" }).end(readFileSync(join(dir, "index.html")));
}).listen(8080, "127.0.0.1", () => console.log("http://127.0.0.1:8080"));
