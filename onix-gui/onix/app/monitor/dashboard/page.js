import styles from "../../page.module.css";

const page = () => {
    const data = [
        {
            ip: "192.168.1.1",
            status: "Live",
            cpu: "2%",
            memory: "18.8 MB",
            uptime: "10 days",
        },
        {
            ip: "10.0.0.1",
            status: "404",
            cpu: "0%",
            memory: "0 MB",
            uptime: "5 days",
        },
        {
            ip: "172.16.0.1",
            status: "Live",
            cpu: "6%",
            memory: "56.4 MB",
            uptime: "15 days",
        },
        {
            ip: "192.0.2.1",
            status: "Live",
            cpu: "69%",
            memory: "648.6 MB",
            uptime: "20 days",
        },
        {
            ip: "198.51.100.1",
            status: "404",
            cpu: "0%",
            memory: "0 MB",
            uptime: "3 days",
        },
        {
            ip: "203.0.113.1",
            status: "Live",
            cpu: ".4%",
            memory: "3.7 MB",
            uptime: "8 days",
        },
    ];
    return (
        <div className={styles.dashboard}>
            <div className={styles.counts}>
                <p
                    style={{
                        textAlign: "left",
                    }}
                    className={styles.count}
                >
                    Uptime Network Participants: 10
                </p>
                <p
                    style={{
                        textAlign: "right",
                    }}
                    className={styles.count}
                >
                    Uptime Network Participants: 10
                </p>
            </div>
            <h2 className={styles.dashboardHeading}>Dashboard</h2>
            <table className={styles.dashboardTable}>
                <thead>
                    <tr>
                        <th>S/N</th>
                        <th>IP / Domain Name</th>
                        <th>Status</th>
                        <th>CPU</th>
                        <th>Memory</th>
                    </tr>
                </thead>
                <tbody>
                    {data.map((item, index) => (
                        <tr key={index}>
                            <td>{index + 1}</td>
                            <td>{item.ip}</td>
                            <td>{item.status}</td>
                            <td>{item.cpu}</td>
                            <td>{item.memory}</td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );
};

export default page;
