package ai.aerol.microvm;

import java.net.http.HttpClient;

public class MicroVMConfig {
    public String patToken;
    public String apiUrl;
    public HttpClient httpClient;
    /**
     * Wire version of the sandbox daemon API to call. Defaults to "v1" when
     * null/empty. The Java SDK package version and the API wire version
     * evolve independently — bumping this SDK does not move the wire
     * version.
     */
    public String apiVersion;
    public ai.aerol.microvm.model.RetryConfig retry;

    public MicroVMConfig setPatToken(String patToken) {
        this.patToken = patToken;
        return this;
    }

    public MicroVMConfig setApiUrl(String apiUrl) {
        this.apiUrl = apiUrl;
        return this;
    }

    /**
     * Supply a custom HTTP client. It <strong>must</strong> be built with
     * {@link HttpClient.Redirect#NEVER}: the SDK follows redirects itself so it
     * can strip {@code Authorization} and {@code X-Registry-*} and refuse a
     * 307/308 body replay on a cross-origin hop. A redirect-following client
     * would replay those credentials inside the JDK instead, so
     * {@link MicroVMClient} rejects one with a {@link MicroVMException} at
     * construction.
     */
    public MicroVMConfig setHttpClient(HttpClient httpClient) {
        this.httpClient = httpClient;
        return this;
    }

    public MicroVMConfig setApiVersion(String apiVersion) {
        this.apiVersion = apiVersion;
        return this;
    }

    public MicroVMConfig setRetry(ai.aerol.microvm.model.RetryConfig retry) {
        this.retry = retry;
        return this;
    }
}