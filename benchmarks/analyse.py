import sqlite3
import pandas as pd
from tabulate import tabulate
import numpy as np
import argparse
import sys

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
    print("Request Latency by Image Tag: \n")
    request_latency = aggregate_and_round(metrics.groupby(['image_tag']), 'grpc_req_duration')

    print(request_latency)
    print("\n")

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



def main():
    parser = argparse.ArgumentParser(
        description='Analyze function metrics from SQLite database')
    parser.add_argument('--db-path', default='metrics.db',
                        help='Path to SQLite database')
    args = parser.parse_args()

    try:
        print_request_latency(args.db_path)
        print_data_transfer(args.db_path)
        print_cold_start_metrics(args.db_path)
    except sqlite3.OperationalError as e:
        print(f"Error accessing database: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"Error during analysis: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
