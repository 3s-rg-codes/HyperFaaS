import sqlite3
import pandas as pd
from tabulate import tabulate
import argparse
from typing import List, Dict
import sys

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
        s.function_id,
        s.instance_id,
        fi.image_tag,
        s.start_time,
        r.running_time,
        (julianday(r.running_time) - julianday(s.start_time)) * 24 * 60 * 60 * 1000 as cold_start_ms
    FROM start_events s
    JOIN running_events r ON s.instance_id = r.instance_id
    JOIN function_images fi ON s.function_id = fi.function_id
    ORDER BY s.function_id, s.instance_id
    """
    
    df = pd.read_sql_query(query, conn)
    conn.close()
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

def print_cold_start_times(db_path: str):
    df = get_cold_start_times(db_path)
    print("\nCold Start Times by Instance:")
    print(tabulate(df, headers='keys', tablefmt='psql', showindex=False))

def print_function_summary(db_path: str):
    df = get_function_summary(db_path)
    print("\nFunction Summary Statistics:")
    print(tabulate(df, headers='keys', tablefmt='psql', showindex=True))

def main():
    parser = argparse.ArgumentParser(description='Analyze function metrics from SQLite database')
    parser.add_argument('--db-path', default='metrics.db', help='Path to SQLite database')
    args = parser.parse_args()

    try:
        print_cold_start_times(args.db_path)
        print_function_summary(args.db_path)
    except sqlite3.OperationalError as e:
        print(f"Error accessing database: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"Error during analysis: {e}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
