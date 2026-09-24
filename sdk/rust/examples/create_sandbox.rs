use aerolvm_sdk::{Client, CreateOptions};
use std::env;

fn main() {
    let api_url = env::args()
        .nth(1)
        .unwrap_or_else(|| "http://127.0.0.1:21212".to_owned());
    // Read the PAT from the environment so it never shows up in shell history
    // or process listings.
    let pat_token =
        env::var("SB_PAT_TOKEN").expect("PAT token is required. Set SB_PAT_TOKEN.");
    let image = env::args().nth(2).expect("Image is required");

    let client = Client::new(Some(&api_url), Some(&pat_token)).expect("failed to create client");
    let health = client.health().expect("health check failed");
    println!("health = {:#?}", health);

    let sandbox = client
        .create(CreateOptions {
            image,
            cpu: Some(1),
            memory_mb: Some(1024),
            disk_gb: Some(10),
            ..Default::default()
        })
        .expect("sandbox creation failed");

    // Sandbox's Debug redacts ssh_private_key and the client PAT.
    println!("sandbox = {:#?}", sandbox);
    println!("open {}", sandbox.data.public_url);
}
