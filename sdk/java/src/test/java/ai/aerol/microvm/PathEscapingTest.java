package ai.aerol.microvm;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import org.junit.jupiter.api.Test;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

/**
 * Dynamic IDs are caller-supplied: a hostile id like "x/../admin" must be
 * percent-escaped into a single path segment ("x%2F..%2Fadmin"), never
 * concatenated raw where it traverses out of its route.
 */
class PathEscapingTest {

    private static final String HOSTILE = "x/../admin";
    private static final String ESCAPED = "x%2F..%2Fadmin";

    @Test
    void resourceIdsArePercentEscapedInPaths() throws Exception {
        List<String> rawPaths = Collections.synchronizedList(new ArrayList<String>());
        HttpServer server = startServer(exchange -> {
            rawPaths.add(exchange.getRequestURI().getRawPath());
            writeJson(exchange, 200, "{}");
        });

        try {
            MicroVMClient client = new MicroVMClient(
                new MicroVMConfig()
                    .setApiUrl("http://127.0.0.1:" + server.getAddress().getPort())
                    .setPatToken("pat-token")
            );
            // Response bodies are irrelevant here; only the request paths matter.
            try { client.getTemplate(HOSTILE); } catch (RuntimeException ignored) { }
            try { client.deleteTemplate(HOSTILE); } catch (RuntimeException ignored) { }
            try { client.getWasmModule(HOSTILE); } catch (RuntimeException ignored) { }
            try { client.deleteWasmModule(HOSTILE); } catch (RuntimeException ignored) { }
            try { client.rebuildTemplate(HOSTILE); } catch (RuntimeException ignored) { }

            List<String> want = new ArrayList<String>();
            want.add("/v1/templates/" + ESCAPED);
            want.add("/v1/templates/" + ESCAPED);
            want.add("/v1/wasm-modules/" + ESCAPED);
            want.add("/v1/wasm-modules/" + ESCAPED);
            want.add("/v1/templates/" + ESCAPED + "/rebuild");
            assertEquals(want, rawPaths);
        } finally {
            server.stop(0);
        }
    }

    private static HttpServer startServer(ThrowingHandler handler) throws IOException {
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/", exchange -> {
            try {
                handler.handle(exchange);
            } finally {
                exchange.close();
            }
        });
        server.start();
        return server;
    }

    private static void writeJson(HttpExchange exchange, int status, String body) throws IOException {
        byte[] payload = body.getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().add("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, payload.length);
        try (OutputStream out = exchange.getResponseBody()) {
            out.write(payload);
        }
    }

    private interface ThrowingHandler {
        void handle(HttpExchange exchange) throws IOException;
    }
}
