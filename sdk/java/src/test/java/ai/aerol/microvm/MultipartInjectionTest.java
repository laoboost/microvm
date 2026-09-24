package ai.aerol.microvm;

import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import org.junit.jupiter.api.Test;

/**
 * The hand-rolled multipart encoder writes targetPath raw into the part body
 * and splices its basename into {@code filename="..."} — CR/LF or quotes there
 * enable MIME part/header injection. Hostile input must be rejected before
 * anything is encoded.
 */
class MultipartInjectionTest {

    @Test
    void uploadFileRejectsCrlfInTargetPath() {
        MicroVMClient client = new MicroVMClient(
            new MicroVMConfig().setApiUrl("http://127.0.0.1:1").setPatToken("pat-token")
        );
        String hostile = "/workspace/ok.txt\r\n--deadbeef\r\nContent-Disposition: form-data; name=\"pwn\"";
        MicroVMException ex = assertThrows(MicroVMException.class,
            () -> client.uploadFile("sb-1", hostile, new byte[] {1}));
        assertTrue(ex.getMessage().contains("CR") || ex.getMessage().contains("quote"),
            "validation message should explain the rejection: " + ex.getMessage());
    }

    @Test
    void uploadFileRejectsQuoteInFilename() {
        MicroVMClient client = new MicroVMClient(
            new MicroVMConfig().setApiUrl("http://127.0.0.1:1").setPatToken("pat-token")
        );
        String hostile = "/workspace/evil\".txt";
        MicroVMException ex = assertThrows(MicroVMException.class,
            () -> client.uploadFile("sb-1", hostile, new byte[] {1}));
        // Assert the *validation* message, not just any MicroVMException: the
        // unreachable apiUrl makes every transport failure a MicroVMException
        // too, so a bare assertThrows can never fail on unfixed code.
        assertTrue(ex.getMessage().contains("quote"),
            "validation message should explain the rejection: " + ex.getMessage());
    }
}
