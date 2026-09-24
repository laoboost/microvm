package ai.aerol.microvm.internal;

import java.io.ByteArrayOutputStream;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpResponse;
import java.net.http.WebSocket;
import java.net.http.WebSocketHandshakeException;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.CompletionStage;

import ai.aerol.microvm.MicroVMException;

@SuppressWarnings({"ThrowableResultOfMethodCallIgnored", "ResultOfMethodCallIgnored"})
public final class JavaNetWebSocketConnector implements WebSocketConnector {
    private final HttpClient httpClient;

    public JavaNetWebSocketConnector(HttpClient httpClient) {
        this.httpClient = httpClient;
    }

    @Override
    public StreamingWebSocket connect(URI uri, String authorizationHeader, StreamingWebSocketListener listener) {
        WebSocket.Builder builder = httpClient.newWebSocketBuilder();
        if (authorizationHeader != null && !authorizationHeader.isEmpty()) {
            builder.header("Authorization", authorizationHeader);
        }

        try {
            WebSocket webSocket = builder.buildAsync(uri, new AdapterListener(listener)).join();
            return new AdapterSocket(webSocket);
        } catch (CompletionException ex) {
            Throwable cause = resolveCause(ex);
            throw new MicroVMException(describeConnectFailure(uri, cause), cause);
        }
    }

    // describeConnectFailure pulls HTTP status / body off WebSocketHandshakeException
    // so callers see "websocket connect to <uri> failed: status=502 body='toolbox unavailable'"
    // instead of just "failed to connect websocket".
    private static String describeConnectFailure(URI uri, Throwable cause) {
        if (cause instanceof WebSocketHandshakeException) {
            WebSocketHandshakeException handshake = (WebSocketHandshakeException) cause;
            HttpResponse<?> response = handshake.getResponse();
            int status = response == null ? -1 : response.statusCode();
            String body = "";
            if (response != null) {
                Object rawBody = response.body();
                if (rawBody instanceof byte[]) {
                    body = new String((byte[]) rawBody).trim();
                } else if (rawBody != null) {
                    body = rawBody.toString().trim();
                }
            }
            StringBuilder sb = new StringBuilder("websocket connect to ").append(uri).append(" failed");
            if (status > 0) {
                sb.append(": status=").append(status);
            }
            if (!body.isEmpty()) {
                sb.append(" body=").append(body);
            }
            return sb.toString();
        }
        String message = cause == null || cause.getMessage() == null ? "" : ": " + cause.getMessage();
        return "websocket connect to " + uri + " failed" + message;
    }

    private static final class AdapterSocket implements StreamingWebSocket {
        private final WebSocket webSocket;

        private AdapterSocket(WebSocket webSocket) {
            this.webSocket = webSocket;
        }

        @Override
        public void sendText(String text) {
            join(webSocket.sendText(text, true), "failed to send websocket text frame");
        }

        @Override
        public void sendBinary(ByteBuffer data) {
            join(webSocket.sendBinary(data, true), "failed to send websocket binary frame");
        }

        @Override
        public void sendClose(int statusCode, String reason) {
            join(webSocket.sendClose(statusCode, reason), "failed to close websocket");
        }

        private static void join(CompletableFuture<?> future, String message) {
            try {
                future.join();
            } catch (CompletionException ex) {
                throw new MicroVMException(message, resolveCause(ex));
            }
        }
    }

    private static Throwable resolveCause(CompletionException ex) {
        Throwable cause = ex.getCause();
        return cause == null ? ex : cause;
    }

    private static final class AdapterListener implements WebSocket.Listener {
        // Cap buffered message size so a hostile or buggy peer cannot OOM the
        // client with an unbounded fragmented message. Exceeding it closes the
        // connection with 1009 (message too big).
        private static final int MAX_MESSAGE_BYTES = 32 * 1024 * 1024;

        private final StreamingWebSocketListener listener;
        private final StringBuilder textBuffer = new StringBuilder();
        private final ByteArrayOutputStream binaryBuffer = new ByteArrayOutputStream();
        private int bufferedBytes;

        private AdapterListener(StreamingWebSocketListener listener) {
            this.listener = listener;
        }

        @Override
        public void onOpen(WebSocket webSocket) {
            webSocket.request(1);
        }

        @Override
        public CompletionStage<?> onText(WebSocket webSocket, CharSequence data, boolean last) {
            textBuffer.append(data);
            // Count UTF-8 bytes, not UTF-16 chars: the cap is a byte cap and a
            // non-ASCII message would otherwise undercount by up to 3x.
            bufferedBytes += data.toString().getBytes(StandardCharsets.UTF_8).length;
            if (bufferedBytes > MAX_MESSAGE_BYTES) {
                return rejectOversized(webSocket);
            }
            if (last) {
                listener.onText(textBuffer.toString());
                textBuffer.setLength(0);
                bufferedBytes = 0;
            }
            webSocket.request(1);
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public CompletionStage<?> onBinary(WebSocket webSocket, ByteBuffer data, boolean last) {
            byte[] chunk = new byte[data.remaining()];
            data.get(chunk);
            binaryBuffer.write(chunk, 0, chunk.length);
            bufferedBytes += chunk.length;
            if (bufferedBytes > MAX_MESSAGE_BYTES) {
                return rejectOversized(webSocket);
            }
            if (last) {
                listener.onBinary(binaryBuffer.toByteArray());
                binaryBuffer.reset();
                bufferedBytes = 0;
            }
            webSocket.request(1);
            return CompletableFuture.completedFuture(null);
        }

        private CompletionStage<?> rejectOversized(WebSocket webSocket) {
            textBuffer.setLength(0);
            binaryBuffer.reset();
            bufferedBytes = 0;
            listener.onError(new MicroVMException("websocket message exceeds 32MiB limit"));
            try {
                webSocket.sendClose(1009, "message too large");
            } catch (RuntimeException ignored) {
            }
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public CompletionStage<?> onClose(WebSocket webSocket, int statusCode, String reason) {
            listener.onClose(statusCode, reason);
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public void onError(WebSocket webSocket, Throwable error) {
            listener.onError(error);
        }
    }
}