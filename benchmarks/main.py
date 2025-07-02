import argparse
import sys
import sqlite3

import pandas as pd
from data_loader import Data
from analysis import (
    analyze_request_latency, analyze_data_transfer, analyze_cold_starts, 
    get_function_summary, analyze_k6_scenarios_summary,
    print_request_latency_analysis, print_data_transfer_analysis, 
    print_cold_start_analysis, print_k6_scenarios_analysis,
    print_cold_start_times, print_function_summary
)
from plot import Plotter

def run_database_analysis(metrics: pd.DataFrame, include_cold_starts: bool = False, cold_starts: pd.DataFrame = None):
    """Run analysis on database metrics."""
    print("Loading database metrics...")
    try:
        
        # Request latency analysis
        latency_results = analyze_request_latency(metrics)
        print_request_latency_analysis(latency_results)
        
        # Data transfer analysis
        transfer_results = analyze_data_transfer(metrics)
        print_data_transfer_analysis(transfer_results)
        
        # Cold start analysis (if requested)
        if include_cold_starts:
            cold_start_results = analyze_cold_starts(metrics, cold_starts)
            print_cold_start_analysis(cold_start_results)
            
            function_summary = get_function_summary(cold_starts)
            print_function_summary(function_summary)
            print_cold_start_times(cold_starts)
        
        return metrics, cold_starts if include_cold_starts else None
        
    except sqlite3.OperationalError as e:
        print(f"Error accessing database: {e}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"Error during database analysis: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc(file=sys.stderr)
        sys.exit(1)

def run_scenarios_analysis(scenarios_path: str):
    """Run analysis on k6 scenarios JSON file."""
    print("Loading k6 scenarios...")
    try:
        scenarios_df = load_k6_scenarios(scenarios_path)
        scenarios_results = analyze_k6_scenarios_summary(scenarios_df)
        print_k6_scenarios_analysis(scenarios_results)
        return scenarios_df
        
    except Exception as e:
        print(f"Error during scenarios analysis: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc(file=sys.stderr)
        sys.exit(1)

def run_plotting(metrics=None, scenarios_df=None, scenarios_path=None, show=True, save_path=None):
    """Run plotting operations."""
    if not metrics and not scenarios_df:
        print("No data available for plotting")
        return
        
    plotter = Plotter(show=show, save_path=save_path)
    
    if metrics is not None:
        print("Generating database plots...")
        plotter.plot_throughput_leaf_node(metrics)
        plotter.plot_decomposed_latency(metrics)
    
    if scenarios_df is not None:
        print("Generating scenarios plots...")
        plotter.plot_expected_rps(scenarios_df, scenarios_path)

def main():
    parser = argparse.ArgumentParser(
        description='Analyze function metrics from SQLite database and/or k6 scenarios')
    
    # Data source options
    parser.add_argument('--db-path', default='metrics.db',
                        help='Path to SQLite database (default: metrics.db)')
    parser.add_argument('--scenarios-path', 
                        help='Path to k6 scenarios JSON file (optional)')
    
    # Analysis options
    parser.add_argument('--analysis', action='store_true',
                        help='Run analysis')
    
    # Plotting options
    parser.add_argument('--plot', action='store_true',
                        help='Generate plots')
    parser.add_argument('--plot-save-path', 
                        help='Directory to save plots (optional)')
    parser.add_argument('--show', action='store_true', default=False,
                        help='Show plots interactively')
    parser.add_argument('--prefix',
                        help='Prefix to save plots with (optional)')
    
    args = parser.parse_args()
    
    d = Data()
    plotter = Plotter(show=args.show, save_path=args.plot_save_path, prefix=args.prefix)
    # Run analysis based on arguments
    if args.analysis:
        # Run database analysis
        metrics = d.load_metrics(args.db_path)
        run_database_analysis(metrics, include_cold_starts=False)
        
    if args.scenarios_path:
            scenarios_df = d.load_k6_scenarios(args.scenarios_path)
            plotter.plot_expected_rps(scenarios_df, args.scenarios_path)
    
    # Run plotting if requested
    if args.plot:
        if d.metrics is None:
            metrics = d.load_metrics(args.db_path)
        plotter.plot_throughput_leaf_node(d.metrics)
        plotter.plot_decomposed_latency(d.metrics)

if __name__ == "__main__":
    main() 