import argparse
import os

from microvm import MicroVM


def main() -> None:
    parser = argparse.ArgumentParser(description="Create a sandbox with the Aerol.ai MicroVM Python SDK")
    parser.add_argument("--api-url", required=False, default="http://127.0.0.1:21212")
    parser.add_argument("--image", required=True)
    parser.add_argument("--cpu", type=float, default=1.0)
    parser.add_argument("--memory-mb", type=int, default=1024)
    parser.add_argument("--disk-gb", type=int, default=10)
    args = parser.parse_args()

    pat_token = os.environ.get("SB_PAT_TOKEN", "")
    if not pat_token:
        raise SystemExit("PAT token is required. Set SB_PAT_TOKEN.")

    client = MicroVM(api_url=args.api_url, pat_token=pat_token)
    health = client.health()
    print("health", health)

    sandbox = client.create(
        {
            "image": args.image,
            "cpu": args.cpu,
            "memoryMB": args.memory_mb,
            "diskGB": args.disk_gb,
        }
    )

    sandbox_data = sandbox.to_dict()
    if sandbox_data.get("sshPrivateKey"):
        sandbox_data["sshPrivateKey"] = "***"
    print("sandbox", sandbox_data)
    print(f"open {sandbox.publicURL}")


if __name__ == "__main__":
    main()
