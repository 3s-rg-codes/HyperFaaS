import pandas as pd
import seaborn as sns
import matplotlib.pyplot as plt
import json

IMAGE_PALETTE = {
    "hyperfaas-bfs-json:latest": "blue",
    "hyperfaas-echo:latest": "red",
    "hyperfaas-thumbnailer-json:latest": "green",
}

def plot_throughput_leaf_node(df: pd.DataFrame):
    """Plot the number of requests that completed processing per second at the leaf node level.
    We use requests fully completed as the basis for throughput here.
    """
    print("Plotting requests processed per second at the leaf node level...")
    
    # Define time range based on timestamps
    start_time = df['timestamp'].min().floor('s')
    end_time = df['timestamp'].max().ceil('s')
    time_range = pd.date_range(start=start_time, end=end_time, freq='s')
    
    print(f"Time range: {start_time} to {end_time}")
    print(f"Duration: {end_time - start_time}")
    
    successful_counts = []
    timeout_counts = []
    error_counts = []
    
    for t in time_range:
        requests_in_window = (df['timestamp'] >= t) & (df['timestamp'] < t + pd.Timedelta(seconds=1))
        
        successful_counts.append((requests_in_window & df['grpc_req_duration'].notna()).sum())
        timeout_counts.append((requests_in_window & df['timeout'].notna()).sum())
        error_counts.append((requests_in_window & df['error'].notna()).sum())
    
    plot_data = pd.DataFrame({
        'time': time_range,
        'successful': successful_counts,
        'timeout': timeout_counts,
        'error': error_counts
    })
    
    plot_data['total'] = plot_data['successful'] + plot_data['timeout'] + plot_data['error']
    
    total_successful = df['grpc_req_duration'].notna().sum()
    total_timeouts = df['timeout'].notna().sum()
    total_errors = df['error'].notna().sum()
    total_requests = len(df) 
    if total_requests > 0:
        print(f"Success rate: {total_successful / total_requests * 100:.1f}%")
        print(f"Timeout rate: {total_timeouts / total_requests * 100:.1f}%")
        print(f"Error rate: {total_errors / total_requests * 100:.1f}%")
    
    # 2 plots in 1
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(15, 10), sharex=True)
    
    ax1.fill_between(plot_data['time'], 0, plot_data['successful'], 
                     label='Successful', color='green', alpha=0.7)
    ax1.fill_between(plot_data['time'], plot_data['successful'], 
                     plot_data['successful'] + plot_data['timeout'],
                     label='Timeout', color='orange', alpha=0.7)
    ax1.fill_between(plot_data['time'], plot_data['successful'] + plot_data['timeout'],
                     plot_data['total'],
                     label='Error', color='red', alpha=0.7)
    
    ax1.plot(plot_data['time'], plot_data['total'], 
             color='black', linestyle='-', linewidth=2, label='Total Requests')
    
    ax1.set_title('Leaf Node Throughput: Successful Requests, Timeouts, and Errors Over Time', 
                  fontsize=14, fontweight='bold')
    ax1.set_ylabel('Requests Per Second', fontsize=12)
    ax1.legend(loc='upper right')
    ax1.grid(True, alpha=0.3)
 
    successful_df = df[df['grpc_req_duration'].notna()].copy()
    if not successful_df.empty:
        sns.scatterplot(data=successful_df, x='timestamp', y='grpc_req_duration', 
                       hue='image_tag', palette=IMAGE_PALETTE, alpha=0.6, ax=ax2)
    ax2.set_title("Request Latency Over Time (Successful Requests Only)", fontsize=14, fontweight='bold')
    ax2.set_xlabel('Time', fontsize=12)
    ax2.set_ylabel('Latency (ms)', fontsize=12)
    ax2.grid(True, alpha=0.3)
    
    plt.tight_layout()
    plt.show()

def plot_decomposed_latency(df: pd.DataFrame):
    """Plot the source of latency of requests decomposed by image tag"""
    print("Plotting decomposed latency...")
    # Function processing time (from the function execution itself)
    df['function_processing_ms'] = pd.to_timedelta(df['functionprocessingtime']).dt.total_seconds() * 1000
    
    # Remove rows with missing data
    df = df.dropna(subset=['scheduling_latency_ms', 'leaf_to_worker_latency_ms', 'function_processing_latency_ms', 'image_tag'])
    
    import seaborn.objects as so
    
    df_melted = df.melt(
        id_vars=['image_tag'], 
        value_vars=['scheduling_latency_ms', 'leaf_to_worker_latency_ms', 'function_processing_latency_ms'],
        var_name='latency_type', 
        value_name='latency_ms'
    )
    (
        so.Plot(df_melted, x="image_tag", y="latency_ms", color="latency_type")
        .add(so.Bar(), so.Agg("sum"), so.Norm(func="sum", by=["x"]), so.Stack())
        .layout(size=(12, 6))
        .label(
            title="Latency Decomposition by Image Tag",
            x="Image Tag",
            y="Average Latency (ms)",
            color="Latency Component"
        )
        .show()
    )

def plot_expected_rps(df: pd.DataFrame, scenarios_path: str = None):
    """Plot expected RPS over time for each function image."""
    if df.empty:
        print("No data to plot")
        return
    
    plt.figure(figsize=(15, 8))
    
    # Create the plot for individual functions
    sns.lineplot(data=df, x='second', y='expected_rps', hue='image_tag', 
                marker='o', markersize=4, palette=IMAGE_PALETTE)
    
    # Add total RPS line
    total_rps = df.groupby('second')['expected_rps'].sum().reset_index()
    sns.lineplot(data=total_rps, x='second', y='expected_rps', 
                color='black', linestyle='--', label='Total RPS',
                linewidth=2)
    
    plt.title('Expected Request Rate Over Time (from k6 Scenarios)', fontsize=14, fontweight='bold')
    plt.xlabel('Time (seconds)', fontsize=12)
    plt.ylabel('Expected Requests Per Second', fontsize=12)
    plt.grid(True, alpha=0.3)
    plt.legend(title='Function Image', bbox_to_anchor=(1.05, 1), loc='upper left')
    
    # Add scenario information as subtitle if path provided
    if scenarios_path:
        with open(scenarios_path, 'r') as f:
            metadata = json.load(f)['metadata']
        plt.suptitle(f"Total Duration: {metadata['totalDuration']}, Seed: {metadata['seed']}", 
                    fontsize=10, y=0.98)
    
    plt.tight_layout()
    plt.show()
    
    # Print summary statistics
    print("\nExpected RPS Summary by Image Tag:")
    summary = df.groupby('image_tag')['expected_rps'].agg(['mean', 'max', 'sum']).round(2)
    summary.columns = ['avg_rps', 'peak_rps', 'total_requests']
    print(summary)
    print(f"\nTotal expected requests across all functions: {df['expected_rps'].sum():.0f}")