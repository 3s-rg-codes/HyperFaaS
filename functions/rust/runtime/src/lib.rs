use std::{env, error::Error, future::Future, net::SocketAddr};

use hyper::{Request, Response, body::Incoming, server::conn::http1, service::service_fn};
use hyper_util::rt::TokioIo;
use tokio::net::TcpListener;

pub mod workerpb {
    tonic::include_proto!("hyperfaas");
}

const DEFAULT_FUNCTION_PORT: &str = "50052";

pub async fn ready<F, Fut>(handler: F) -> Result<(), Box<dyn Error + Send + Sync>>
where
    F: Fn(Request<Incoming>) -> Fut + Clone + Send + 'static,
    Fut: Future<Output = Result<Response<String>, hyper::Error>> + Send + 'static,
{
    let controller_address = required_env("CONTROLLER_ADDRESS")?;
    let instance_id = required_env("INSTANCE_ID")?.parse::<u64>()?;
    let function_port = env::var("FUNCTION_PORT").unwrap_or_else(|_| DEFAULT_FUNCTION_PORT.to_string());
    let addr: SocketAddr = format!("0.0.0.0:{function_port}").parse()?;

    let listener = TcpListener::bind(addr).await?;
    signal_ready(controller_address, instance_id).await?;
    println!("hyperfaas rust runtime ready on {function_port}");

    loop {
        let (stream, _) = listener.accept().await?;
        let io = TokioIo::new(stream);
        let handler = handler.clone();
        tokio::spawn(async move {
            if let Err(err) = http1::Builder::new().serve_connection(io, service_fn(handler)).await {
                eprintln!("hyperfaas rust runtime connection error: {err}");
            }
        });
    }
}

fn required_env(name: &str) -> Result<String, Box<dyn Error + Send + Sync>> {
    env::var(name).map_err(|_| format!("hyperfaas runtime: {name} is required").into())
}

async fn signal_ready(controller_address: String, instance_id: u64) -> Result<(), Box<dyn Error + Send + Sync>> {
    let mut client = workerpb::sandbox_service_client::SandboxServiceClient::connect(format!("http://{controller_address}")).await?;
    client.signal_ready(workerpb::SignalReadyRequest { instance_id }).await?;
    Ok(())
}
