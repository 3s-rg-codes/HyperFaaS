import csv
import sqlite3
from collections import defaultdict
import re
import json

def create_tables(conn):
    cursor = conn.cursor()
    
    # one row per request is the idea
    cursor.execute('''
    CREATE TABLE IF NOT EXISTS metrics (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        timestamp INTEGER, -- timestamp of the logging of the request
        scenario TEXT,
        service TEXT,
        image_tag TEXT,
        instance_id TEXT,
        request_id TEXT,
        
        -- Request timing metrics
        grpc_req_duration REAL,
        callqueuedtimestamp REAL,
        gotresponsetimestamp REAL,
        functionprocessingtime REAL,
        leafgotrequesttimestamp REAL,
        leafscheduledcalltimestamp REAL,
        timeout REAL, -- timestamp of the timeout of the request
        error REAL, -- timestamp of the error of the request
        
        -- Data metrics
        data_sent REAL,
        data_received REAL,
        iteration_duration REAL,
        function_parameters TEXT,
        
        -- Other metadata
        proto TEXT,
        subproto TEXT,
        group_name TEXT,
        extra_tags TEXT,
        
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    )
    ''')
    
    # Create function_images table
    cursor.execute('''
    CREATE TABLE IF NOT EXISTS function_images (
        function_id TEXT PRIMARY KEY,
        image_tag TEXT
    )
    ''')
    
    conn.commit()

def parse_tags(tags_str):
    """Parse key-value pairs from a string like 'key1=value1&key2=value2'"""
    if not tags_str:
        return {}
    
    result = {}
    # Handle cases where the tag might have equals signs in its value
    pairs = re.findall(r'([^&=]+)=([^&]*)', tags_str)
    for key, value in pairs:
        result[key] = value
    
    return result

def import_function_images(conn, json_file='generated_scenarios.json'):
    """Import function IDs and their image tags from the scenarios JSON file"""
    with open(json_file, 'r') as f:
        data = json.load(f)
    
    cursor = conn.cursor()
    
    # Extract function IDs and image tags from metadata
    function_images = [
        (data['metadata']['bfsFunctionId'], data['metadata']['configuration']['BFS_IMAGE_TAG']),
        (data['metadata']['echoFunctionId'], data['metadata']['configuration']['ECHO_IMAGE_TAG']),
        (data['metadata']['thumbnailerFunctionId'], data['metadata']['configuration']['THUMBNAILER_IMAGE_TAG'])
    ]
    
    # Insert or replace function images
    cursor.executemany('''
    INSERT OR REPLACE INTO function_images (function_id, image_tag)
    VALUES (?, ?)
    ''', function_images)
    
    conn.commit()
    
    # Print statistics
    print("\nImported function images:")
    cursor.execute("SELECT * FROM function_images")
    for row in cursor.fetchall():
        print(f"  Function {row[0]}: {row[1]}")

def import_csv_to_sqlite(csv_file='test_results.csv', db_file='metrics.db', json_file='generated_scenarios.json'):
    conn = sqlite3.connect(db_file)
    create_tables(conn)
    
    # First import function images from JSON
    import_function_images(conn, json_file)
    
    cursor = conn.cursor()

    # to collect metrics for each request
    requests = defaultdict(dict)
    
    # collect all metrics by request_id
    with open(csv_file, 'r') as file:
        csv_reader = csv.DictReader(file)
        for row in csv_reader:
            # Skip setup rows
            if '::setup' in row['group']:
                continue
                
            # Skip dropped iterations
            if row['metric_name'] == 'dropped_iterations':
                continue
            
            # Extract request identifier
            scenario = row['scenario']
            metadata = row['metadata']
            
            # Skip rows without proper metadata or scenario
            if not scenario or not metadata:
                continue
                
            # The metadata can be used as a request_id
            # Transform metadata string into consistent request_key format
            metadata_dict = {k: v for part in metadata.split('&') for k, v in [part.split('=')]}
            request_key = f"vu={metadata_dict['vu']}&iter={metadata_dict['iter']}"
            
            # Store common metadata for this request
            requests[request_key]['scenario'] = scenario
            requests[request_key]['service'] = row['service']
            requests[request_key]['timestamp'] = row['timestamp']
            requests[request_key]['request_id'] = request_key
            
            # Parse extra_tags to extract image_tag
            if row['extra_tags']:
                tags = parse_tags(row['extra_tags'])
                if 'image_tag' in tags:
                    requests[request_key]['image_tag'] = tags['image_tag']
                if 'instanceId' in tags:
                    requests[request_key]['instance_id'] = tags['instanceId']
                if 'functionProcessingTime' in tags:
                    requests[request_key]['function_processing_time'] = tags['functionProcessingTime']
                if 'functionParameters' in tags:
                    requests[request_key]['function_parameters'] = tags['functionParameters']
                    
            # Store proto and subproto info
            requests[request_key]['proto'] = row['proto']
            requests[request_key]['subproto'] = row['subproto']
            requests[request_key]['group_name'] = row['group']
            requests[request_key]['extra_tags'] = row['extra_tags']
            
            # Store callqueuedtimestamp, gotresponsetimestamp, data_sent, data_received, iteration_duration
            if row['metric_name'] == 'callqueuedtimestamp':
                requests[request_key]['callqueuedtimestamp'] = row['metric_value']
            elif row['metric_name'] == 'gotresponsetimestamp':
                requests[request_key]['gotresponsetimestamp'] = row['metric_value']
            elif row['metric_name'] == 'data_sent':
                requests[request_key]['data_sent'] = row['metric_value']
            elif row['metric_name'] == 'data_received':
                requests[request_key]['data_received'] = row['metric_value']
            elif row['metric_name'] == 'iteration_duration':
                requests[request_key]['iteration_duration'] = row['metric_value']
            elif row['metric_name'] == 'grpc_req_duration':
                requests[request_key]['grpc_req_duration'] = row['metric_value']
            elif row['metric_name'] == 'leafgotrequesttimestamp':
                requests[request_key]['leafgotrequesttimestamp'] = row['metric_value']
            elif row['metric_name'] == 'leafscheduledcalltimestamp':
                requests[request_key]['leafscheduledcalltimestamp'] = row['metric_value']
            elif row['metric_name'] == 'timeout':
                requests[request_key]['timeout'] = row['metric_value']
            elif row['metric_name'] == 'error':
                requests[request_key]['error'] = row['metric_value']
    # insert collected requests into the database
    for request_key, data in requests.items():
            
        cursor.execute('''
        INSERT INTO metrics (
            timestamp, scenario, service, image_tag, instance_id, request_id,
            grpc_req_duration, callqueuedtimestamp, gotresponsetimestamp, functionprocessingtime,
            leafgotrequesttimestamp, leafscheduledcalltimestamp,
            data_sent, data_received, iteration_duration, function_parameters,
            proto, subproto, group_name, extra_tags, timeout, error
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ''', (
            data.get('timestamp'),
            data.get('scenario'),
            data.get('service'),
            data.get('image_tag'),
            data.get('instance_id'),
            data.get('request_id'),
            data.get('grpc_req_duration'),
            data.get('callqueuedtimestamp'),
            data.get('gotresponsetimestamp'),
            data.get('function_processing_time'),
            data.get('leafgotrequesttimestamp'),
            data.get('leafscheduledcalltimestamp'),
            data.get('data_sent'),
            data.get('data_received'),
            data.get('iteration_duration'),
            data.get('function_parameters'),
            data.get('proto'),
            data.get('subproto'),
            data.get('group_name'),
            data.get('extra_tags'),
            data.get('timeout'),
            data.get('error'),
        ))
    
    conn.commit()
    
    # Print statistics
    cursor.execute("SELECT COUNT(*) FROM metrics")
    count = cursor.fetchone()[0]
    print(f"Imported {count} unique requests into the database")
    
    # Sample query to verify data
    cursor.execute("""
    SELECT scenario, COUNT(*), AVG(grpc_req_duration) as avg_duration 
    FROM metrics 
    GROUP BY scenario
    """)
    print("\nScenario statistics:")
    for row in cursor.fetchall():
        try:
            print(f"  {row[0]}: {row[1]} requests, avg duration: {row[2]:.3f}ms")
        except:
            pass
    
    conn.close()

if __name__ == "__main__":
    import argparse
    
    parser = argparse.ArgumentParser(description='Import metrics from CSV to SQLite')
    parser.add_argument('--csv', default='test_results.csv', help='Path to CSV file')
    parser.add_argument('--db', default='metrics.db', help='Path to SQLite database')
    parser.add_argument('--json', default='generated_scenarios.json', help='Path to scenarios JSON file')
    
    args = parser.parse_args()
    import_csv_to_sqlite(args.csv, args.db, args.json)