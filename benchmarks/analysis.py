import pandas as pd
from tabulate import tabulate

def aggregate_and_round(df: pd.DataFrame, col: str) -> pd.DataFrame:
    """Aggregate and round the dataframe."""
    r = df.agg({
        col: ['count','mean', 'min', 'max',
              lambda x: x.quantile(0.50),
              lambda x: x.quantile(0.75),
              lambda x: x.quantile(0.95),
              lambda x: x.quantile(0.99)]
    }).round(2)
    r.columns = ['count','mean', 'min', 'max', 'p50', 'p75', 'p95', 'p99']
    return r

def analyze_request_latency(metrics: pd.DataFrame) -> dict:
    """Analyze request latency metrics and return results as dictionary."""
    results = {}
    
    results['total_requests'] = metrics['request_id'].nunique()
    results['total_successful'] = metrics['grpc_req_duration'].count()
    
    # Request latency by image tag
    request_latency = aggregate_and_round(metrics.groupby(['image_tag']), 'grpc_req_duration')
    results['latency_by_image'] = request_latency
    
    # Timeouts by image tag
    timeout_by_image = metrics.groupby(['image_tag'])['timeout'].count()
    results['timeouts_by_image'] = timeout_by_image
    results['total_timeouts'] = metrics['timeout'].count()
    
    # Errors by image tag
    error_by_image = metrics.groupby(['image_tag'])['error'].count()
    results['errors_by_image'] = error_by_image
    results['total_errors'] = metrics['error'].count()
    
    # Request latency by scenario and image tag
    request_latency_scenario = aggregate_and_round(metrics.groupby(['scenario', 'image_tag']), 'grpc_req_duration')
    results['latency_by_scenario_image'] = request_latency_scenario
    
    return results

def analyze_data_transfer(metrics: pd.DataFrame) -> dict:
    """Analyze data transfer metrics and return results as dictionary."""
    results = {}
    
    # Data sent by image tag
    data_sent = aggregate_and_round(metrics.groupby(['image_tag']), 'data_sent')
    results['data_sent_by_image'] = data_sent
    
    # Data received by image tag
    data_received = aggregate_and_round(metrics.groupby(['image_tag']), 'data_received')
    results['data_received_by_image'] = data_received
    
    return results

def analyze_cold_starts(metrics: pd.DataFrame, cold_starts: pd.DataFrame) -> dict:
    """Analyze cold start metrics and return results as dictionary."""
    results = {}
    
    # Merge metrics with cold starts on instance_id
    metrics_by_instance = metrics[['callqueuedtimestamp', 'gotresponsetimestamp', 'instance_id', 'grpc_req_duration']]
    cold_starts_with_metrics = pd.merge(cold_starts, metrics_by_instance, on='instance_id', how='left')
    
    # Cold starts per image
    cold_starts_per_image = aggregate_and_round(cold_starts_with_metrics.groupby('image_tag'), 'cold_start_ms')
    results['cold_starts_by_image'] = cold_starts_per_image
    
    # Total request latency for cold starts
    total_request_latency = aggregate_and_round(cold_starts_with_metrics.groupby('image_tag'), 'grpc_req_duration')
    results['cold_start_request_latency'] = total_request_latency
    
    return results

def get_function_summary(cold_starts: pd.DataFrame) -> pd.DataFrame:
    """Get summary statistics for each function."""
    summary = cold_starts.groupby('function_id').agg({
        'cold_start_ms': ['mean', 'min', 'max', 'std',
                          lambda x: x.quantile(0.50),
                          lambda x: x.quantile(0.75),
                          lambda x: x.quantile(0.95)],
        'instance_id': 'count'
    }).round(2)

    # Flatten multi-index columns
    summary.columns = ['avg_ms', 'min_ms', 'max_ms', 'std_ms',
                       'p50_ms', 'p75_ms', 'p95_ms', 'count']

    return summary

def analyze_k6_scenarios_summary(scenarios_df: pd.DataFrame) -> dict:
    """Analyze k6 scenarios data and return summary statistics."""
    results = {}
    
    # Summary by image tag
    summary = scenarios_df.groupby('image_tag')['expected_rps'].agg(['mean', 'max', 'sum']).round(2)
    summary.columns = ['avg_rps', 'peak_rps', 'total_requests']
    results['summary_by_image'] = summary
    
    # Total expected requests
    results['total_expected_requests'] = scenarios_df['expected_rps'].sum()
    
    return results

def print_request_latency_analysis(results: dict):
    """Print request latency analysis results."""
    print("Total requests:")
    print(results['total_requests'])
    print()
    
    print("Total requests served successfully:")
    print(results['total_successful'])
    print()
    
    print("Request Latency by Image Tag:")
    print(results['latency_by_image'])
    print()

    print("Total timeout requests by image tag:")
    print(results['timeouts_by_image'])
    print(f"Total timeouts: {results['total_timeouts']}")
    print()

    print("Total errors by image tag:")
    print("Note: these errors are LeafNode/Worker errors, not function errors")
    print(results['errors_by_image'])
    print(f"Total errors: {results['total_errors']}")
    print()

    print("Request Latency by Scenario and Image Tag:")
    print(results['latency_by_scenario_image'])
    print()

def print_data_transfer_analysis(results: dict):
    """Print data transfer analysis results."""
    print("Data Sent by Image Tag (Bytes):")
    print(results['data_sent_by_image'])
    print()

    print("Data Received by Image Tag (Bytes):")
    print(results['data_received_by_image'])
    print()

def print_cold_start_analysis(results: dict):
    """Print cold start analysis results."""
    print("\n WIP WIP WIP WIP WIP WIP !!! \n")
    
    print("Cold Start in milliseconds by Image Tag:")
    print(results['cold_starts_by_image'])
    print()

    print("Total Request latency for those cold starts:")
    print(results['cold_start_request_latency'])
    print()

def print_k6_scenarios_analysis(results: dict):
    """Print k6 scenarios analysis results."""
    print("Expected RPS Summary by Image Tag:")
    print(results['summary_by_image'])
    print(f"\nTotal expected requests across all functions: {results['total_expected_requests']:.0f}")

def print_cold_start_times(cold_starts: pd.DataFrame):
    """Print cold start times by instance."""
    print("\nCold Start Times by Instance:")
    print(tabulate(cold_starts, headers='keys', tablefmt='psql', showindex=False))

def print_function_summary(function_summary: pd.DataFrame):
    """Print function summary statistics."""
    print("\nFunction Summary Statistics:")
    print(tabulate(function_summary, headers='keys', tablefmt='psql', showindex=True)) 