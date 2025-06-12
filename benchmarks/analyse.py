import sqlite3
import pandas as pd
from tabulate import tabulate
import argparse
import sys
import json
from plot import *

metrics = None

def get_cold_start_times(db_path: str) -> pd.DataFrame:
    """Calculate cold start times for each function instance."""
    conn = sqlite3.connect(db_path)

    query = """
    WITH start_events AS (
        SELECT 
            instance_id,
            function_id,
            timestamp as start_time
        FROM status_updates
        WHERE event = 3 AND status = 0  -- EVENT_START, STATUS_SUCCESS
    ),
    running_events AS (
        SELECT 
            instance_id,
            function_id,
            timestamp as running_time
        FROM status_updates
        WHERE event = 6 AND status = 0  -- EVENT_RUNNING, STATUS_SUCCESS
    )
    SELECT 
        s.function_id as function_id,
        s.instance_id as instance_id,
        fi.image_tag as image_tag,
        s.start_time as start_time,
        r.running_time as running_time,
        (julianday(r.running_time) - julianday(s.start_time)) * 24 * 60 * 60 * 1000 as cold_start_ms
    FROM start_events s
    JOIN running_events r ON s.instance_id = r.instance_id
    JOIN function_images fi ON s.function_id = fi.function_id
    ORDER BY s.function_id, s.instance_id
    """

    df = pd.read_sql_query(query, conn)
    conn.close()
    return df

def get_metrics(db_path: str) -> pd.DataFrame:
    """Get request latency for each function."""
    global metrics
    if metrics is not None:
        return metrics
    
    conn = sqlite3.connect(db_path)
    
    query = """
    SELECT
        *
    FROM metrics
    """
    
    df = pd.read_sql_query(query, conn)
    conn.close()
    df['callqueuedtimestamp'] = pd.to_datetime(df['callqueuedtimestamp'], unit='ns')
    df['leafgotrequesttimestamp'] = pd.to_datetime(df['leafgotrequesttimestamp'], unit='ns')
    df['leafscheduledcalltimestamp'] = pd.to_datetime(df['leafscheduledcalltimestamp'], unit='ns')
    df['gotresponsetimestamp'] = pd.to_datetime(df['gotresponsetimestamp'], unit='ns')
    
    # Manual workaround for pandas datetime conversion bug with NaN values
    #https://github.com/pandas-dev/pandas/pull/61022
    #https://github.com/pandas-dev/pandas/issues/58419
    # Convert timeout column
    timeout_series = df['timeout']
    timeout_valid_mask = timeout_series.notna()
    timeout_result = pd.Series(index=timeout_series.index, dtype='datetime64[ns]')
    if timeout_valid_mask.any():
        timeout_result.loc[timeout_valid_mask] = pd.to_datetime(timeout_series[timeout_valid_mask], unit='ms')
    df['timeout'] = timeout_result
    
    # Convert error column  
    error_series = df['error']
    error_valid_mask = error_series.notna()
    error_result = pd.Series(index=error_series.index, dtype='datetime64[ns]')
    if error_valid_mask.any():
        error_result.loc[error_valid_mask] = pd.to_datetime(error_series[error_valid_mask], unit='ms')
    df['error'] = error_result
    
    df['timestamp'] = pd.to_datetime(df['timestamp'], unit='s')
    # Add latency
    df['scheduling_latency_ms'] = (df['leafscheduledcalltimestamp'] - df['leafgotrequesttimestamp']).dt.total_seconds() * 1000
    df['leaf_to_worker_latency_ms'] = (df['callqueuedtimestamp'] - df['leafscheduledcalltimestamp']).dt.total_seconds() * 1000
    df['function_processing_latency_ms'] = (df['gotresponsetimestamp'] - df['callqueuedtimestamp']).dt.total_seconds() * 1000
    metrics = df
    return df
    
def get_function_summary(db_path: str) -> pd.DataFrame:
    """Get summary statistics for each function."""
    cold_starts = get_cold_start_times(db_path)

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

def print_request_latency(db_path: str):
    """ print request latency for each function and other metrics"""
    metrics = get_metrics(db_path)
    print("Total requests: \n")
    print(metrics['request_id'].nunique())
    print("\n")
    
    print("Total requests served successfully: \n")
    print(metrics['grpc_req_duration'].count())
    print("\n")
    
    print("Request Latency by Image Tag: \n")
    request_latency = aggregate_and_round(metrics.groupby(['image_tag']), 'grpc_req_duration')
    print(request_latency)
    print("\n")

    print("Total timeout requests by image tag: \n")
    print(metrics.groupby(['image_tag'])['timeout'].count())
    print("Total timeouts:")
    print(metrics['timeout'].count())

    print("Total errors by image tag: \n")
    print("Note: these errors are LeafNode/Worker errors, not function errors")
    print(metrics.groupby(['image_tag'])['error'].count())
    print("Total errors:")
    print(metrics['error'].count())

    print("Request Latency by Scenario and Image Tag: \n")
    # Filter for grpc_req_duration metrics and calculate latency percentiles
    request_latency = metrics.groupby(['scenario', 'image_tag'])
    request_latency = aggregate_and_round(request_latency, 'grpc_req_duration')

    print(request_latency)
    print("\n")

def print_data_transfer(db_path: str):
    """ print data transfer for each function and other metrics"""
    metrics = get_metrics(db_path)
    # we have 2 metrics for data transfer: data_sent and data_received
    # we want to print the mean of both
    print("Data Sent by Image Tag (Bytes): \n")
    data_sent = metrics.groupby(['image_tag'])
    data_sent = aggregate_and_round(data_sent, 'data_sent')

    print(data_sent)
    print("\n")

    print("Data Received by Image Tag (Bytes): \n")
    data_received = metrics.groupby(['image_tag'])
    data_received = aggregate_and_round(data_received, 'data_received')

    print(data_received)
    print("\n")

def print_cold_start_metrics(db_path: str):
    """ print cold start metrics for each function"""
    metrics = get_metrics(db_path)
    cold_starts = get_cold_start_times(db_path)
    # This is a bit trickier . We have some additional metrics that help us:
    # - callqueuedtimestamp
    # - gotresponsetimestamp
    # - instanceid
        # - the instanceid metric will have the actual instance id inside of the extra_tags.
        # - this is ugly but due to the fact that metric values can only be numbers, booleans or timestamps. No strings.
        # - we can parse the extra_tags to get the instance id
    # So, for each call, we have :
    # - total latency = grpc_req_duration
    # - cold start time = running event timestamp - start event timestamp . they exist if the cold start happened.
    # - function execution time = gotresponsetimestamp - callqueuedtimestamp
    
    # merge both dataframes on instance_id ?
    # if we group metrics by request_id, we will have the instance_id available in each entry
    print("\n WIP WIP WIP WIP WIP WIP !!! \n")
    metrics_by_instance = metrics[['callqueuedtimestamp', 'gotresponsetimestamp', 'instance_id', 'grpc_req_duration']]
    # merge with cold_starts on instance_id
    cold_starts_with_metrics = pd.merge(cold_starts, metrics_by_instance, on='instance_id', how='left')

    cold_starts_per_image = cold_starts_with_metrics.groupby('image_tag')
    cold_starts_per_image = aggregate_and_round(cold_starts_per_image, 'cold_start_ms')

    print("\n Cold Start in milliseconds by Image Tag: \n")
    print(cold_starts_per_image)


    total_request_latency = cold_starts_with_metrics.groupby('image_tag')
    total_request_latency = aggregate_and_round(total_request_latency, 'grpc_req_duration')
    print("\n Total Request latency for those cold starts: \n")
    print(total_request_latency)

def print_cold_start_times(db_path: str):
    df = get_cold_start_times(db_path)
    print("\nCold Start Times by Instance:")
    print(tabulate(df, headers='keys', tablefmt='psql', showindex=False))

def print_function_summary(db_path: str):
    df = get_function_summary(db_path)
    print("\nFunction Summary Statistics:")
    print(tabulate(df, headers='keys', tablefmt='psql', showindex=True))

def aggregate_and_round(df: pd.DataFrame,col: str) -> pd.DataFrame:
    """ aggregate and round the dataframe"""
    r = df.agg({
        col: ['count','mean', 'min', 'max',
              lambda x: x.quantile(0.50),
              lambda x: x.quantile(0.75),
              lambda x: x.quantile(0.95),
              lambda x: x.quantile(0.99)]
    }).round(2)
    r.columns =  ['count','mean', 'min', 'max', 'p50', 'p75', 'p95', 'p99']
    return r

# Calculates RPS per image tag for generated scenarios.
# Assumes a single stage per scenario
def analyze_k6_scenarios(scenarios_path: str) -> pd.DataFrame:
    """Parse k6 scenarios JSON and create a timeline dataframe of expected RPS."""
    with open(scenarios_path, 'r') as f:
        data = json.load(f)
    
    scenarios = data['scenarios']
    
    total_duration_str = data['metadata']['totalDuration']
    if total_duration_str.endswith('m'):
        total_duration_seconds = int(total_duration_str.rstrip('m')) * 60
    elif total_duration_str.endswith('s'):
        total_duration_seconds = int(total_duration_str.rstrip('s'))
    else:
        raise ValueError(f"Invalid total duration: {total_duration_str}")
    
    timeline_data = []
    
    # we need to calculate the expected rps for each second
    for second in range(total_duration_seconds):
        rps_by_image = {}
        
        for scenario_name, scenario in scenarios.items():
            image_tag = scenario['tags']['image_tag']
            
            start_time_str = scenario['startTime']
            start_time = int(start_time_str.rstrip('s'))
            
            #  duration
            if scenario['executor'] == 'constant-arrival-rate':
                duration = int(scenario['duration'].rstrip('s'))
                end_time = start_time + duration
                
                # Check if this second falls within the scenario duration
                if start_time <= second < end_time:
                    rate = scenario['rate']
                    if image_tag not in rps_by_image:
                        rps_by_image[image_tag] = 0
                    rps_by_image[image_tag] += rate
                    
            elif scenario['executor'] == 'ramping-arrival-rate':
                # we need to calculate the rate for this specific second
                start_rate = scenario['startRate']
                stage = scenario['stages'][0]  # Important: we assume a single stage !
                target_rate = stage['target']
                stage_duration = int(stage['duration'].rstrip('s'))
                end_time = start_time + stage_duration
                
                # Check if this second falls within the scenario duration
                if start_time <= second < end_time:
                    progress = (second - start_time) / stage_duration
                    current_rate = start_rate + (target_rate - start_rate) * progress
                    
                    if image_tag not in rps_by_image:
                        rps_by_image[image_tag] = 0
                    rps_by_image[image_tag] += current_rate
        
        # Add entries for each image tag at this second
        for image_tag, rps in rps_by_image.items():
            timeline_data.append({
                'second': second,
                'image_tag': image_tag,
                'expected_rps': rps
            })
        
        # If no scenarios are active for any image at this second, add zero entries
        if not rps_by_image:
            # Get all unique image tags from scenarios
            all_image_tags = set(scenario['tags']['image_tag'] for scenario in scenarios.values())
            for image_tag in all_image_tags:
                timeline_data.append({
                    'second': second,
                    'image_tag': image_tag,
                    'expected_rps': 0
                })
    
    df = pd.DataFrame(timeline_data)
    
    # Fill missing combinations with 0
    if not df.empty:
        all_seconds = range(total_duration_seconds)
        all_image_tags = df['image_tag'].unique()
        
        # Create complete index
        complete_index = pd.MultiIndex.from_product([all_seconds, all_image_tags], 
                                                   names=['second', 'image_tag'])
        complete_df = pd.DataFrame(index=complete_index).reset_index()
        
        # Merge with actual data
        df = complete_df.merge(df, on=['second', 'image_tag'], how='left')
        df['expected_rps'] = df['expected_rps'].fillna(0)
    
    return df

def main():
    parser = argparse.ArgumentParser(
        description='Analyze function metrics from SQLite database')
    parser.add_argument('--db-path', default='metrics.db',
                        help='Path to SQLite database')
    parser.add_argument('--scenarios-path', 
                        help='Path to k6 scenarios JSON file (optional)')
    parser.add_argument('--plot', help='Plot the metrics', default=False)
    args = parser.parse_args()

    try:
        print_request_latency(args.db_path)
        print_data_transfer(args.db_path)
        #print_cold_start_metrics(args.db_path)
        if args.plot:
            plot_throughput_leaf_node(metrics)
            #plot_requests_processed_per_second(metrics)
            #plot_throughput_vs_latency_over_time(metrics)
            plot_decomposed_latency(metrics)
        
            if args.scenarios_path:
                    scenarios_df = analyze_k6_scenarios(args.scenarios_path)
                    plot_expected_rps(scenarios_df, args.scenarios_path)
            
    except sqlite3.OperationalError as e:
        print(f"Error accessing database: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"Error during analysis: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc(file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
