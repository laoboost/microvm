package ai.aerol.microvm.internal;

import java.lang.reflect.Constructor;
import java.lang.reflect.Method;
import java.net.http.WebSocket;
import java.nio.ByteBuffer;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionStage;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import org.junit.jupiter.api.Test;

/**
 * Unbounded WebSocket message buffering lets a hostile or buggy peer OOM the
 * client. Fragmented messages larger than 32 MiB must fail the connection
 * (close 1009) instead of being buffered and delivered.
 */
class JavaNetWebSocketConnectorTest {

    @Test
    void oversizedFragmentedTextMessageIsRejectedWithClose1009() throws Exception {
        RecordingStreamingWebSocketListener recording = new RecordingStreamingWebSocketListener();
        RecordingWebSocket socket = new RecordingWebSocket();

        Object adapter = newAdapterListener(recording);
        Method onText = adapter.getClass()
            .getDeclaredMethod("onText", WebSocket.class, CharSequence.class, boolean.class);
        onText.setAccessible(true);

        // Three 12 MiB fragments add up to 36 MiB — past the 32 MiB cap.
        String fragment = "x".repeat(12 * 1024 * 1024);
        for (int i = 0; i < 2; i++) {
            onText.invoke(adapter, socket, fragment, false);
        }
        onText.invoke(adapter, socket, fragment, true);

        assertEquals(1, socket.closeCalls.size(), "oversized message must close the socket exactly once");
        assertEquals(1009, socket.closeCalls.get(0).intValue(), "oversized message must close with 1009");
        assertTrue(recording.texts.isEmpty(), "oversized message must not be delivered: got "
            + recording.texts.size() + " text message(s)");
        assertFalse(recording.errors.isEmpty(), "oversized message must surface an error callback");
    }

    @Test
    void oversizedFragmentedBinaryMessageIsRejectedWithClose1009() throws Exception {
        RecordingStreamingWebSocketListener recording = new RecordingStreamingWebSocketListener();
        RecordingWebSocket socket = new RecordingWebSocket();

        Object adapter = newAdapterListener(recording);
        Method onBinary = adapter.getClass()
            .getDeclaredMethod("onBinary", WebSocket.class, ByteBuffer.class, boolean.class);
        onBinary.setAccessible(true);

        byte[] fragment = new byte[12 * 1024 * 1024];
        for (int i = 0; i < 2; i++) {
            onBinary.invoke(adapter, socket, ByteBuffer.wrap(fragment), false);
        }
        onBinary.invoke(adapter, socket, ByteBuffer.wrap(fragment), true);

        assertEquals(1, socket.closeCalls.size(), "oversized message must close the socket exactly once");
        assertEquals(1009, socket.closeCalls.get(0).intValue(), "oversized message must close with 1009");
        assertTrue(recording.binaries.isEmpty(), "oversized message must not be delivered");
        assertFalse(recording.errors.isEmpty(), "oversized message must surface an error callback");
    }

    private static Object newAdapterListener(StreamingWebSocketListener listener) throws Exception {
        Class<?> clazz =
            Class.forName("ai.aerol.microvm.internal.JavaNetWebSocketConnector$AdapterListener");
        Constructor<?> ctor = clazz.getDeclaredConstructor(StreamingWebSocketListener.class);
        ctor.setAccessible(true);
        return ctor.newInstance(listener);
    }

    private static final class RecordingStreamingWebSocketListener implements StreamingWebSocketListener {
        final List<String> texts = new ArrayList<String>();
        final List<byte[]> binaries = new ArrayList<byte[]>();
        final List<Throwable> errors = new ArrayList<Throwable>();

        @Override
        public void onText(String text) {
            texts.add(text);
        }

        @Override
        public void onBinary(byte[] data) {
            binaries.add(data);
        }

        @Override
        public void onClose(int statusCode, String reason) {
            // not under test here
        }

        @Override
        public void onError(Throwable error) {
            errors.add(error);
        }
    }

    private static final class RecordingWebSocket implements WebSocket {
        final List<Integer> closeCalls = new ArrayList<Integer>();

        @Override
        public String getSubprotocol() {
            return "";
        }

        @Override
        public CompletableFuture<WebSocket> sendText(CharSequence data, boolean last) {
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public CompletableFuture<WebSocket> sendBinary(ByteBuffer data, boolean last) {
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public CompletableFuture<WebSocket> sendPing(ByteBuffer message) {
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public CompletableFuture<WebSocket> sendPong(ByteBuffer message) {
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public CompletableFuture<WebSocket> sendClose(int statusCode, String reason) {
            closeCalls.add(statusCode);
            return CompletableFuture.completedFuture(null);
        }

        @Override
        public void request(long n) {
            // no-op
        }

        @Override
        public boolean isOutputClosed() {
            return false;
        }

        @Override
        public boolean isInputClosed() {
            return false;
        }

        @Override
        public void abort() {
            // no-op
        }
    }
}
