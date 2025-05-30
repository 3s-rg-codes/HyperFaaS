import pandas as pd
import numpy as np
import seaborn as sns
import matplotlib.pyplot as plt

IMAGE_PALETTE = {
    "hyperfaas-bfs-json:latest": "blue",
    "hyperfaas-echo:latest": "red",
    "hyperfaas-thumbnailer-json:latest": "green",
}

def prepare_dataframe(df: pd.DataFrame, use_gotresponsetimestamp=True):
    """Helper function to prepare dataframe with timestamp conversions"""
    # Drop rows with missing required values
    if use_gotresponsetimestamp:
        df = df[df['callqueuedtimestamp'].notna() & df['gotresponsetimestamp'].notna()].copy()
    else:
        df = df[df['callqueuedtimestamp'].notna() & df['functionprocessingtime'].notna()].copy()
    
    # Convert timestamps from nanoseconds to datetime
    df['callqueuedtimestamp'] = pd.to_datetime(df['callqueuedtimestamp'], unit='ns')
    df['leafgotrequesttimestamp'] = pd.to_datetime(df['leafgotrequesttimestamp'], unit='ns')
    df['leafscheduledcalltimestamp'] = pd.to_datetime(df['leafscheduledcalltimestamp'], unit='ns')
    df['gotresponsetimestamp'] = pd.to_datetime(df['gotresponsetimestamp'], unit='ns')
    # Add latency
    df['scheduling_latency_ms'] = (df['leafscheduledcalltimestamp'] - df['leafgotrequesttimestamp']).dt.total_seconds() * 1000
    df['lead_to_worker_latency_ms'] = (df['callqueuedtimestamp'] - df['leafscheduledcalltimestamp']).dt.total_seconds() * 1000
    df['function_processing_latency_ms'] = (df['gotresponsetimestamp'] - df['callqueuedtimestamp']).dt.total_seconds() * 1000
    return df

# This function is not working as intended. But it makes sense: most requests are processed super quickly.
# This means that for the way the calculation is done, the number of inflight requests is super low.
def plot_inflight_requests(df: pd.DataFrame):
    # Debug: Check raw timestamp values before conversion
    print("Sample raw callqueuedtimestamp values:")
    print(df['callqueuedtimestamp'].head())
    print(f"Min raw timestamp: {df['callqueuedtimestamp'].min()}")
    print(f"Max raw timestamp: {df['callqueuedtimestamp'].max()}")
    
    # Prepare dataframe
    df = prepare_dataframe(df, use_gotresponsetimestamp=True)
    
    # Debug: Print actual time range
    raw_start = df['callqueuedtimestamp'].min()
    raw_end = df['gotresponsetimestamp'].max()
    print(f"Processed start time: {raw_start}")
    print(f"Processed end time: {raw_end}")
    print(f"Time difference: {raw_end - raw_start}")
    
    # Define time range per second
    start_time = df['callqueuedtimestamp'].min().floor('s')
    end_time = df['gotresponsetimestamp'].max().ceil('s')
    
    print(f"Floored start time: {start_time}")
    print(f"Ceiled end time: {end_time}")
    print(f"Expected duration: {end_time - start_time}")
    
    # Create time range - should be reasonable for your test duration
    time_range = pd.date_range(start=start_time, end=end_time, freq='s')
    print(f"Time range length: {len(time_range)} seconds")
    
    # Count in-flight requests per second
    inflight_counts = [
        ((df['callqueuedtimestamp'] <= t) &
         (df['gotresponsetimestamp'] > t)).sum()
        for t in time_range
    ]
    
    # Create and plot the series
    series = pd.Series(inflight_counts, index=time_range, name='inflight_rps')
    print("\n Time Series: \n")
    print(series)
    
    plt.figure(figsize=(12, 5))
    sns.lineplot(x=series.index, y=series.values)
    plt.title("In-Flight Requests Per Second")
    plt.xlabel("Time")
    plt.ylabel("Number of In-Flight Requests")
    plt.tight_layout()
    plt.show()

def plot_requests_processed_per_second(df: pd.DataFrame):
    """Plot the number of requests that completed processing per second"""
    print("Plotting requests processed per second...")
    
    # Prepare dataframe
    df = prepare_dataframe(df, use_gotresponsetimestamp=True)
    
    # Debug: Print actual time range
    raw_start = df['callqueuedtimestamp'].min()
    raw_end = df['gotresponsetimestamp'].max()
    print(f"Processed start time: {raw_start}")
    print(f"Processed end time: {raw_end}")
    print(f"Time difference: {raw_end - raw_start}")
    print(f"Total requests: {len(df)}")
    
    # Define time range per second based on when requests completed
    start_time = df['gotresponsetimestamp'].min().floor('s')
    end_time = df['gotresponsetimestamp'].max().ceil('s')
    
    print(f"Floored start time: {start_time}")
    print(f"Ceiled end time: {end_time}")
    print(f"Expected duration: {end_time - start_time}")
    
    # Create time range
    time_range = pd.date_range(start=start_time, end=end_time, freq='s')
    print(f"Time range length: {len(time_range)} seconds")
    
    # Count requests that completed in each second
    processed_counts = [
        ((df['gotresponsetimestamp'] > t) & (df['gotresponsetimestamp'] <= t + pd.Timedelta(seconds=1))).sum()
        for t in time_range
    ]
    
    # Create and plot the series
    series = pd.Series(processed_counts, index=time_range, name='processed_rps')
    print(f"\nProcessed requests stats:")
    print(f"Min: {series.min()}, Max: {series.max()}, Mean: {series.mean():.2f}")
    print(f"Total processed: {series.sum()}")
    
    plt.figure(figsize=(12, 5))
    sns.lineplot(x=series.index, y=series.values)
    plt.title("Requests Processed Per Second")
    plt.xlabel("Time")
    plt.ylabel("Number of Requests Completed")
    plt.tight_layout()
    plt.show()

def plot_throughput_vs_latency_over_time(df: pd.DataFrame):
    """Plot requests processed per second and latency over time in stacked subplots"""
    print("Plotting throughput vs latency over time...")
    
    # Prepare dataframe
    df = prepare_dataframe(df, use_gotresponsetimestamp=True)
    
    # Calculate requests processed per second
    start_time = df['gotresponsetimestamp'].min().floor('s')
    end_time = df['gotresponsetimestamp'].max().ceil('s')
    time_range = pd.date_range(start=start_time, end=end_time, freq='s')
    
    processed_counts = [
        ((df['gotresponsetimestamp'] > t) & (df['gotresponsetimestamp'] <= t + pd.Timedelta(seconds=1))).sum()
        for t in time_range
    ]
    
    throughput_series = pd.Series(processed_counts, index=time_range, name='processed_rps')
    
    # Create stacked subplots
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(15, 10), sharex=True)
    
    # Top plot: Requests processed per second
    sns.lineplot(x=throughput_series.index, y=throughput_series.values, ax=ax1)
    ax1.set_title("Requests Processed Per Second")
    ax1.set_ylabel("Requests/Second")
    ax1.grid(True, alpha=0.3)
    
    # Bottom plot: Latency scatter plot
    df['latency'] = df['grpc_req_duration']
    sns.scatterplot(data=df, x='callqueuedtimestamp', y='latency', hue='image_tag', 
                   palette=IMAGE_PALETTE, alpha=0.6, ax=ax2)
    ax2.set_title("Request Latency Over Time")
    ax2.set_xlabel("Time")
    ax2.set_ylabel("Latency (ms)")
    ax2.grid(True, alpha=0.3)
    
    plt.tight_layout()
    plt.show()
    
    # Print correlation info
    print(f"\nThroughput Statistics:")
    print(f"Mean RPS: {throughput_series.mean():.2f}")
    print(f"Max RPS: {throughput_series.max()}")
    print(f"Min RPS: {throughput_series.min()}")

def plot_decomposed_latency(df: pd.DataFrame):
    """Plot the source of latency of requests decomposed by image tag"""
    print("Plotting decomposed latency...")
    
    # Prepare dataframe
    df = prepare_dataframe(df, use_gotresponsetimestamp=True)
    
    # Function processing time (from the function execution itself)
    df['function_processing_ms'] = pd.to_timedelta(df['functionprocessingtime']).dt.total_seconds() * 1000
    
    # Remove rows with missing data
    df = df.dropna(subset=['scheduling_latency_ms', 'lead_to_worker_latency_ms', 'function_processing_latency_ms', 'image_tag'])
    
    import seaborn.objects as so
    
    df_melted = df.melt(
        id_vars=['image_tag'], 
        value_vars=['scheduling_latency_ms', 'lead_to_worker_latency_ms', 'function_processing_latency_ms'],
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