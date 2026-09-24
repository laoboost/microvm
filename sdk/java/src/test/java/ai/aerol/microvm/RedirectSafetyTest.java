package ai.aerol.microvm;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.http.HttpClient;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.atomic.AtomicInteger;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import org.junit.jupiter.api.Test;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

import ai.aerol.microvm.model.BuildImageOptions;
import ai.aerol.microvm.model.BuildImagePushOptions;
import ai.aerol.microvm.model.PushWasmModuleOptions;

/**
 * Cross-origin redirect hygiene: a 3xx must never carry Authorization or the
 * X-Registry-* credentials to another origin, and a 307/308 must never replay
 * a credential-bearing body cross-origin. Redirects are followed manually (up
 * to 5 hops) with those rules applied.
 */
class RedirectSafetyTest {

    @Test
    void crossOriginRedirectIsFollowedWithoutAuthorizationOrRegistryHeaders() throws Exception {
        ConcurrentLinkedQueue<Map<String, List<String>>> seen =
            new ConcurrentLinkedQueue<Map<String, List<String>>>();
        AtomicInteger targetHits = new AtomicInteger();

        HttpServer target = startServer(exchange -> {
            targetHits.incrementAndGet();
            seen.add(exchange.getRequestHeaders());
            writeJson(exchange, 200, "{\"module_ref\":\"oci://x\",\"digest\":\"d\",\"size_bytes\":1}");
        });
        HttpServer redirector = startServer(exchange -> {
            exchange.getResponseHeaders().add("Location", serverUrl(target) + "/v1/wasm-modules/push");
            exchange.sendResponseHeaders(302, -1);
        });

        try {
            MicroVMClient client = clientFor(redirector);
            client.pushWasmModule(new PushWasmModuleOptions()
                .setName("mod")
                .setTag("latest")
                .setModule(new byte[] {1, 2, 3})
                .setRegistryUsername("registry-user")
                .setRegistryToken("registry-token-secret"));

            assertEquals(1, targetHits.get(), "redirect should be followed exactly once");
            Map<String, List<String>> headers = seen.peek();
            assertFalse(headers.containsKey("Authorization"), "cross-origin redirect leaked Authorization");
            assertFalse(headers.containsKey("X-Registry-Token"), "cross-origin redirect leaked X-Registry-Token");
            assertFalse(headers.containsKey("X-Registry-Username"), "cross-origin redirect leaked X-Registry-Username");
        } finally {
            redirector.stop(0);
            target.stop(0);
        }
    }

    @Test
    void crossOrigin307RefusesToReplayCredentialBodies() throws Exception {
        String secret = "super-secret-push-password";
        List<String> bodies = Collections.synchronizedList(new ArrayList<String>());
        AtomicInteger targetHits = new AtomicInteger();

        HttpServer target = startServer(exchange -> {
            targetHits.incrementAndGet();
            bodies.add(new String(exchange.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
            writeJson(exchange, 200, "{\"image\":\"built:latest\"}");
        });
        HttpServer redirector = startServer(exchange -> {
            exchange.getResponseHeaders().add("Location", serverUrl(target) + "/v1/images/build");
            exchange.sendResponseHeaders(307, -1);
        });

        try {
            MicroVMClient client = clientFor(redirector);
            BuildImagePushOptions push = new BuildImagePushOptions();
            push.registry = "ghcr.io/acme/app";
            push.username = "acme";
            push.password = secret;

            boolean refused = false;
            try {
                client.buildImage(Image.base("alpine"), new BuildImageOptions().setPush(push));
            } catch (RuntimeException ex) {
                refused = true;
            }
            for (String body : bodies) {
                assertFalse(body.contains(secret), "cross-origin redirect replayed a body containing credentials");
            }
            assertTrue(refused || targetHits.get() == 0,
                "cross-origin 307 with a credential-bearing body was followed; want refusal");
        } finally {
            redirector.stop(0);
            target.stop(0);
        }
    }

    @Test
    void sameOriginRedirectKeepsAuthorization() throws Exception {
        List<String> auths = Collections.synchronizedList(new ArrayList<String>());
        AtomicInteger hits = new AtomicInteger();
        HttpServer server = startServer(exchange -> {
            hits.incrementAndGet();
            if ("/health".equals(exchange.getRequestURI().getPath())) {
                exchange.getResponseHeaders().add("Location", "/health2");
                exchange.sendResponseHeaders(302, -1);
                return;
            }
            auths.add(exchange.getRequestHeaders().getFirst("Authorization"));
            writeJson(exchange, 200, "{\"status\":\"ok\"}");
        });

        try {
            MicroVMClient client = clientFor(server);
            client.health();
            assertEquals(2, hits.get(), "same-origin redirect should be followed");
            assertEquals("Bearer pat-token", auths.get(0), "same-origin redirect dropped Authorization");
        } finally {
            server.stop(0);
        }
    }

    /**
     * A caller-supplied {@link HttpClient} whose policy follows redirects would
     * replay Authorization (and X-Registry-*) to the redirect target inside the
     * JDK, before the SDK's manual policy runs. The client must refuse it, so
     * the second origin receives nothing.
     */
    @Test
    void injectedRedirectFollowingHttpClientCannotReachTheRedirectTarget() throws Exception {
        ConcurrentLinkedQueue<Map<String, List<String>>> seen =
            new ConcurrentLinkedQueue<Map<String, List<String>>>();
        AtomicInteger targetHits = new AtomicInteger();

        HttpServer target = startServer(exchange -> {
            targetHits.incrementAndGet();
            seen.add(exchange.getRequestHeaders());
            writeJson(exchange, 200, "{\"status\":\"ok\"}");
        });
        HttpServer redirector = startServer(exchange -> {
            exchange.getResponseHeaders().add("Location", serverUrl(target) + "/health");
            exchange.sendResponseHeaders(302, -1);
        });

        try {
            MicroVMConfig config = new MicroVMConfig()
                .setApiUrl(serverUrl(redirector))
                .setPatToken("pat-token")
                .setHttpClient(HttpClient.newBuilder().followRedirects(HttpClient.Redirect.NORMAL).build());
            try {
                MicroVMClient client = new MicroVMClient(config);
                client.health();
            } catch (MicroVMException expected) {
                // Fail closed: the redirect-following client was refused.
            }
            for (Map<String, List<String>> headers : seen) {
                assertNull(headers.get("Authorization"),
                    "injected redirect-following client leaked Authorization to the second origin");
            }
            assertEquals(0, targetHits.get(),
                "injected redirect-following client reached the redirect target");
        } finally {
            redirector.stop(0);
            target.stop(0);
        }
    }

    private static MicroVMClient clientFor(HttpServer server) {
        return new MicroVMClient(
            new MicroVMConfig().setApiUrl(serverUrl(server)).setPatToken("pat-token")
        );
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

    private static String serverUrl(HttpServer server) {
        return "http://127.0.0.1:" + server.getAddress().getPort();
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
